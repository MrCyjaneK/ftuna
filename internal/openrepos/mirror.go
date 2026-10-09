package openrepos

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

const (
	sailfishOrigin = "https://sailfish.openrepos.net"
	keyURL         = sailfishOrigin + "/openrepos.key"
	keyObject      = "mirror/openrepos/openrepos.key"
	manifestKey    = "index/manifest.json"
	shardKey       = "index/shard-0001.json"
)

type objectRec struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Cache  string `json:"cache"`
}

type publisherState struct {
	Publisher      string      `json:"publisher"`
	UID            string      `json:"uid"`
	SyncedAt       string      `json:"synced_at"`
	RepomdSHA256   string      `json:"repomd_sha256"`
	RepomdETag     string      `json:"repomd_etag,omitempty"`
	KeyFingerprint string      `json:"key_fingerprint"`
	PackageCount   int         `json:"package_count"`
	Uploaded       int         `json:"uploaded"`
	Unchanged      int         `json:"unchanged"`
	Deleted        int         `json:"deleted"`
	Objects        []objectRec `json:"objects"`
}

type manifestDoc struct {
	Version   int        `json:"version"`
	Generated string     `json:"generated"`
	Shards    []shardRef `json:"shards"`
}

type shardRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Count  int    `json:"count"`
}

type shardDoc struct {
	Objects []objectRec `json:"objects"`
}

// ErrNoRepo means the publisher has no Sailfish repository to mirror.
// The directory is missing, or primary.xml lists no packages.
var ErrNoRepo = errors.New("no sailfish repository")

// Mirror copies one Sailfish publisher repository onto S3 without rewriting
// repository bytes. The OpenRepos signing key is stored beside it and is
// refused if it no longer matches the key already in the bucket.
func Mirror(ctx context.Context, store *s3store.Store, publisher string, force bool) error {
	if err := validPublisher(publisher); err != nil {
		return err
	}
	user, err := requirePublisher(ctx, store, publisher)
	if err != nil {
		return err
	}
	if err := mirrorPublisher(ctx, store, user, force); err != nil {
		return err
	}
	return PublishCatalog(ctx, store)
}

func mirrorPublisher(ctx context.Context, store *s3store.Store, user userRecord, force bool) error {
	publisher := user.Name
	if err := validPublisher(publisher); err != nil {
		return err
	}

	prev, err := loadPublisherState(ctx, store, publisher)
	if err != nil {
		return err
	}
	client := httpClient()
	repomdURL := publisherURL(publisher, "personal/main/repodata/repomd.xml")
	match := ""
	if prev != nil && !force {
		match = prev.RepomdETag
	}
	got, err := fetch(ctx, client, repomdURL, match)
	if err != nil {
		return err
	}
	if got.Status == http.StatusNotFound {
		return ErrNoRepo
	}
	log.Printf("publisher %s uid %s", user.Name, user.UID)
	if got.Status == http.StatusNotModified {
		log.Printf("%s repomd.xml not modified", publisher)
		return nil
	}
	repomd := got.Body
	repomdSum := sha256.Sum256(repomd)
	repomdSHA := hex.EncodeToString(repomdSum[:])
	if !force && prev != nil && prev.RepomdSHA256 == repomdSHA {
		log.Printf("%s repomd.xml matches the stored copy", publisher)
		if got.ETag != "" && got.ETag != prev.RepomdETag {
			prev.RepomdETag = got.ETag
			if err := writePublisherState(ctx, store, prev); err != nil {
				return err
			}
		}
		return nil
	}

	key, err := downloadBytes(ctx, client, keyURL)
	if err != nil {
		return err
	}
	asc, err := downloadBytes(ctx, client, repomdURL+".asc")
	if err != nil {
		return err
	}
	fingerprint, err := verifyDetached(key, repomd, asc)
	if err != nil {
		return err
	}
	log.Printf("repomd.xml signature ok, key fingerprint %s", fingerprint)

	if err := pinKey(ctx, store, key); err != nil {
		return err
	}
	md, err := parseRepoMD(repomd)
	if err != nil {
		return err
	}
	if !hasType(md, "primary") {
		return fmt.Errorf("repomd.xml has no primary metadata")
	}

	var temps []string
	defer func() {
		for _, p := range temps {
			os.Remove(p)
		}
	}()

	type staged struct {
		rel   string
		path  string
		cache string
	}
	var meta []staged
	var primaryPath, primaryHref string
	for _, d := range md.Data {
		if d.Checksum == "" || d.OpenChecksum == "" {
			return fmt.Errorf("%s is missing checksums", d.Href)
		}
		fileURL := publisherURL(publisher, "personal/main/"+d.Href)
		tmp, hw, err := download(ctx, client, fileURL)
		if err != nil {
			return err
		}
		temps = append(temps, tmp)
		got, err := writtenSum(hw, d.ChecksumType)
		if err != nil {
			return fmt.Errorf("%s: %w", d.Href, err)
		}
		if err := checkSum(d.ChecksumType, got, d.Checksum); err != nil {
			return fmt.Errorf("%s: %w", d.Href, err)
		}
		openSum, err := openSum(tmp, d.Href, d.OpenType)
		if err != nil {
			return fmt.Errorf("%s open-checksum: %w", d.Href, err)
		}
		if err := checkSum(d.OpenType, openSum, d.OpenChecksum); err != nil {
			return fmt.Errorf("%s open-checksum: %w", d.Href, err)
		}
		if d.Type == "primary" {
			primaryPath = tmp
			primaryHref = d.Href
		}
		meta = append(meta, staged{rel: d.Href, path: tmp, cache: "short"})
		log.Printf("verified %s", d.Href)
	}

	pkgs, err := parsePrimary(primaryPath, primaryHref)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		log.Printf("%s primary.xml lists no packages", publisher)
		return ErrNoRepo
	}
	if err := guardShrink(ctx, store, publisher, len(pkgs)); err != nil {
		return err
	}

	desired := map[string]objectRec{}
	uploaded, unchanged := 0, 0
	for _, p := range pkgs {
		key := repoKey(publisher, p.Href)
		head, ok, err := store.Head(ctx, key)
		if err != nil {
			return err
		}
		if ok && strings.EqualFold(head.SHA1, p.Checksum) && head.SHA256 != "" && (p.Size == 0 || head.Size == p.Size) {
			desired[key] = objectRec{Path: key, Size: head.Size, SHA256: head.SHA256, Cache: "immutable"}
			unchanged++
			log.Printf("keep %s", p.Href)
			continue
		}
		fileURL := publisherURL(publisher, "personal/main/"+p.Href)
		res, err := uploadPackage(ctx, client, store, fileURL, key, p)
		if err != nil {
			return err
		}
		desired[key] = objectRec{Path: key, Size: res.Size, SHA256: res.SHA256, Cache: "immutable"}
		uploaded++
		verb := "upload"
		if ok && head.SHA1 != "" && !strings.EqualFold(head.SHA1, p.Checksum) {
			verb = "replace"
		}
		log.Printf("%s %s (%d bytes)", verb, p.Href, res.Size)
	}

	for _, s := range meta {
		key := repoKey(publisher, s.rel)
		res, err := store.PutFile(ctx, key, contentType(s.rel), s.path, nil)
		if err != nil {
			return err
		}
		desired[key] = objectRec{Path: key, Size: res.Size, SHA256: res.SHA256, Cache: s.cache}
	}

	repomdKey := repoKey(publisher, "repodata/repomd.xml")
	ascKey := repomdKey + ".asc"
	ascRes, err := store.PutBytes(ctx, ascKey, "application/pgp-signature", asc, nil)
	if err != nil {
		return err
	}
	repomdRes, err := store.PutBytes(ctx, repomdKey, "application/xml", repomd, nil)
	if err != nil {
		return err
	}
	desired[ascKey] = objectRec{Path: ascKey, Size: ascRes.Size, SHA256: ascRes.SHA256, Cache: "short"}
	desired[repomdKey] = objectRec{Path: repomdKey, Size: repomdRes.Size, SHA256: repomdRes.SHA256, Cache: "short"}

	repoURL := publisherURL(publisher, "personal-main.repo")
	repoBody, err := downloadBytes(ctx, client, repoURL)
	if err != nil {
		return err
	}
	repoRes, err := store.PutBytes(ctx, repoFileKey(publisher), "text/plain", repoBody, nil)
	if err != nil {
		return err
	}
	desired[repoFileKey(publisher)] = objectRec{Path: repoFileKey(publisher), Size: repoRes.Size, SHA256: repoRes.SHA256, Cache: "short"}

	keyRes, err := store.PutBytes(ctx, keyObject, "application/pgp-keys", key, nil)
	if err != nil {
		return err
	}
	desired[keyObject] = objectRec{Path: keyObject, Size: keyRes.Size, SHA256: keyRes.SHA256, Cache: "short"}

	deleted, err := deleteStale(ctx, store, publisher, desired)
	if err != nil {
		return err
	}
	if err := verifyStored(ctx, store, publisher, key, repomd, asc, repoBody, md, pkgs); err != nil {
		return fmt.Errorf("stored mirror does not match what was uploaded: %w", err)
	}

	state := publisherState{
		Publisher:      publisher,
		UID:            user.UID,
		SyncedAt:       time.Now().UTC().Format(time.RFC3339),
		RepomdSHA256:   repomdRes.SHA256,
		RepomdETag:     got.ETag,
		KeyFingerprint: fingerprint,
		PackageCount:   len(pkgs),
		Uploaded:       uploaded,
		Unchanged:      unchanged,
		Deleted:        deleted,
		Objects:        sortedRecs(desired),
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	stateKey := "meta/openrepos/publishers/" + publisher + ".json"
	stateBody := append(body, '\n')
	stateRes, err := store.PutBytes(ctx, stateKey, "application/json", stateBody, nil)
	if err != nil {
		return err
	}
	recorded := append(state.Objects, objectRec{
		Path: stateKey, Size: stateRes.Size, SHA256: stateRes.SHA256, Cache: "short",
	})
	if head, ok, err := store.Head(ctx, usersKey); err != nil {
		return err
	} else if ok && head.SHA256 != "" {
		recorded = append(recorded, objectRec{
			Path: usersKey, Size: head.Size, SHA256: head.SHA256, Cache: "short",
		})
	}
	if err := mergeManifest(ctx, store, recorded, publisherPrefix(publisher), desired); err != nil {
		return err
	}
	log.Printf("mirrored %s: %d packages, %d uploaded, %d unchanged, %d deleted", publisher, len(pkgs), uploaded, unchanged, deleted)
	return nil
}

func uploadPackage(ctx context.Context, client *http.Client, store *s3store.Store, fileURL, key string, p pkgRef) (s3store.PutResult, error) {
	tmp, hw, err := download(ctx, client, fileURL)
	if err != nil {
		return s3store.PutResult{}, err
	}
	defer os.Remove(tmp)
	got, err := writtenSum(hw, p.SumType)
	if err != nil {
		return s3store.PutResult{}, fmt.Errorf("%s: %w", p.Href, err)
	}
	if err := checkSum(p.SumType, got, p.Checksum); err != nil {
		return s3store.PutResult{}, fmt.Errorf("%s: %w", p.Href, err)
	}
	if p.Size > 0 && hw.Size() != p.Size {
		return s3store.PutResult{}, fmt.Errorf("%s: size %d, metadata says %d", p.Href, hw.Size(), p.Size)
	}
	return store.PutFile(ctx, key, "application/x-rpm", tmp, nil)
}

func validPublisher(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\?#\x00") {
		return fmt.Errorf("invalid publisher %q", name)
	}
	return nil
}

func publisherURL(publisher, rel string) string {
	u := sailfishOrigin + "/" + url.PathEscape(publisher)
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return u + "/" + strings.Join(parts, "/")
}

func pinKey(ctx context.Context, store *s3store.Store, key []byte) error {
	existing, ok, err := store.GetIfExists(ctx, keyObject)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !bytes.Equal(existing, key) {
		return fmt.Errorf("openrepos.key on S3 does not match %s; refusing to mirror", keyURL)
	}
	return nil
}

func loadPublisherState(ctx context.Context, store *s3store.Store, publisher string) (*publisherState, error) {
	body, ok, err := store.GetIfExists(ctx, "meta/openrepos/publishers/"+publisher+".json")
	if err != nil || !ok {
		return nil, err
	}
	var st publisherState
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("parse publisher state: %w", err)
	}
	return &st, nil
}

func writePublisherState(ctx context.Context, store *s3store.Store, st *publisherState) error {
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	key := "meta/openrepos/publishers/" + st.Publisher + ".json"
	res, err := store.PutBytes(ctx, key, "application/json", append(body, '\n'), nil)
	if err != nil {
		return err
	}
	return mergeManifest(ctx, store, []objectRec{{
		Path: key, Size: res.Size, SHA256: res.SHA256, Cache: "short",
	}}, "", nil)
}

func guardShrink(ctx context.Context, store *s3store.Store, publisher string, count int) error {
	body, ok, err := store.GetIfExists(ctx, "meta/openrepos/publishers/"+publisher+".json")
	if err != nil || !ok {
		return err
	}
	var prev publisherState
	if err := json.Unmarshal(body, &prev); err != nil {
		return err
	}
	if prev.PackageCount > 0 && count*2 < prev.PackageCount {
		return fmt.Errorf("package count fell from %d to %d; refusing to replace the mirror", prev.PackageCount, count)
	}
	return nil
}

func hasType(md repoMD, typ string) bool {
	for _, d := range md.Data {
		if d.Type == typ {
			return true
		}
	}
	return false
}

func writtenSum(hw *s3store.HashWriter, sumType string) (string, error) {
	switch strings.ToLower(sumType) {
	case "sha", "sha1":
		return hw.SHA1(), nil
	case "sha256":
		return hw.SHA256(), nil
	default:
		return "", fmt.Errorf("unsupported checksum type %q", sumType)
	}
}

func contentType(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".xml"):
		return "application/xml"
	case strings.HasSuffix(rel, ".gz"):
		return "application/gzip"
	case strings.HasSuffix(rel, ".rpm"):
		return "application/x-rpm"
	default:
		return "application/octet-stream"
	}
}

func deleteStale(ctx context.Context, store *s3store.Store, publisher string, desired map[string]objectRec) (int, error) {
	keys, err := store.List(ctx, publisherPrefix(publisher))
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, key := range keys {
		if _, ok := desired[key]; ok {
			continue
		}
		if err := store.Delete(ctx, key); err != nil {
			return deleted, err
		}
		deleted++
		log.Printf("delete %s", key)
	}
	return deleted, nil
}

func verifyStored(ctx context.Context, store *s3store.Store, publisher string, key, repomd, asc, repoBody []byte, md repoMD, pkgs []pkgRef) error {
	gotKey, err := store.Get(ctx, keyObject)
	if err != nil {
		return err
	}
	if !bytes.Equal(gotKey, key) {
		return fmt.Errorf("stored openrepos.key differs from the key that verified the signature")
	}
	gotRepomd, err := store.Get(ctx, repoKey(publisher, "repodata/repomd.xml"))
	if err != nil {
		return err
	}
	if !bytes.Equal(gotRepomd, repomd) {
		return fmt.Errorf("stored repomd.xml differs from origin")
	}
	gotAsc, err := store.Get(ctx, repoKey(publisher, "repodata/repomd.xml.asc"))
	if err != nil {
		return err
	}
	if !bytes.Equal(gotAsc, asc) {
		return fmt.Errorf("stored repomd.xml.asc differs from origin")
	}
	if _, err := verifyDetached(gotKey, gotRepomd, gotAsc); err != nil {
		return err
	}
	for _, d := range md.Data {
		head, ok, err := store.Head(ctx, repoKey(publisher, d.Href))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("missing %s", d.Href)
		}
		got, err := headSum(head, d.ChecksumType)
		if err != nil {
			return fmt.Errorf("%s: %w", d.Href, err)
		}
		if err := checkSum(d.ChecksumType, got, d.Checksum); err != nil {
			return fmt.Errorf("stored %s: %w", d.Href, err)
		}
	}
	for _, p := range pkgs {
		head, ok, err := store.Head(ctx, repoKey(publisher, p.Href))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("missing %s", p.Href)
		}
		if p.Size > 0 && head.Size != p.Size {
			return fmt.Errorf("stored %s size %d, metadata says %d", p.Href, head.Size, p.Size)
		}
		got, err := headSum(head, p.SumType)
		if err != nil {
			return err
		}
		if err := checkSum(p.SumType, got, p.Checksum); err != nil {
			return fmt.Errorf("stored %s: %w", p.Href, err)
		}
	}
	gotRepo, err := store.Get(ctx, repoFileKey(publisher))
	if err != nil {
		return err
	}
	if !bytes.Equal(gotRepo, repoBody) {
		return fmt.Errorf("stored personal-main.repo differs from origin")
	}
	return nil
}

func headSum(h s3store.ObjectHead, sumType string) (string, error) {
	switch strings.ToLower(sumType) {
	case "sha", "sha1":
		if h.SHA1 == "" {
			return "", fmt.Errorf("object has no sha1 metadata")
		}
		return h.SHA1, nil
	case "sha256":
		if h.SHA256 == "" {
			return "", fmt.Errorf("object has no sha256 metadata")
		}
		return h.SHA256, nil
	default:
		return "", fmt.Errorf("unsupported checksum type %q", sumType)
	}
}

func sortedRecs(desired map[string]objectRec) []objectRec {
	out := make([]objectRec, 0, len(desired))
	for _, rec := range desired {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func mergeManifest(ctx context.Context, store *s3store.Store, upsert []objectRec, dropPrefix string, keep map[string]objectRec) error {
	byPath := map[string]objectRec{}
	if body, ok, err := store.GetIfExists(ctx, shardKey); err != nil {
		return err
	} else if ok {
		var shard shardDoc
		if err := json.Unmarshal(body, &shard); err != nil {
			return fmt.Errorf("parse %s: %w", shardKey, err)
		}
		for _, obj := range shard.Objects {
			byPath[obj.Path] = obj
		}
	}
	for path := range byPath {
		if dropPrefix == "" || !strings.HasPrefix(path, dropPrefix) {
			continue
		}
		if _, still := keep[path]; !still {
			delete(byPath, path)
		}
	}
	for _, obj := range upsert {
		byPath[obj.Path] = obj
	}
	// The signing key is updated with the publisher but lives outside the prefix.
	shard := shardDoc{Objects: make([]objectRec, 0, len(byPath))}
	for _, obj := range byPath {
		shard.Objects = append(shard.Objects, obj)
	}
	sort.Slice(shard.Objects, func(i, j int) bool { return shard.Objects[i].Path < shard.Objects[j].Path })
	body, err := json.MarshalIndent(shard, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	sum := sha256.Sum256(body)
	if _, err := store.PutBytes(ctx, shardKey, "application/json", body, nil); err != nil {
		return err
	}
	man := manifestDoc{
		Version:   1,
		Generated: time.Now().UTC().Format(time.RFC3339),
		Shards:    []shardRef{{Path: shardKey, SHA256: hex.EncodeToString(sum[:]), Count: len(shard.Objects)}},
	}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	_, err = store.PutBytes(ctx, manifestKey, "application/json", append(mb, '\n'), nil)
	return err
}
