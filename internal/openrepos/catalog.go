package openrepos

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mrcyjanek/ftuna/internal/aibadge"
	"github.com/mrcyjanek/ftuna/internal/store/s3store"
	"github.com/mrcyjanek/ftuna/internal/xzjson"
)

const (
	catalogKey    = "mirror/openrepos/info.json.xz"
	catalogPublic = "https://repo-openrepos.ftuna.eu/info.json.xz"
	statePrefix   = "meta/openrepos/publishers/"
)

// catalogSource is one mirrored publisher repository.
type catalogSource struct {
	Publisher string
	Updated   time.Time
	RPMs      []pkgRef
}

type catalogDoc struct {
	Version      int           `json:"version"`
	Updated      string        `json:"updated"`
	Categories   []string      `json:"categories"`
	Repositories []catalogRepo `json:"repositories"`
	Packages     []catalogPkg  `json:"packages"`
}

type catalogRepo struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type catalogPkg struct {
	ID          string        `json:"id"`
	Category    string        `json:"category"`
	Updated     string        `json:"updated"`
	Summary     string        `json:"summary"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	Screenshots []string      `json:"screenshots"`
	Repo        string        `json:"repo"`
	Status      catalogStatus `json:"status"`
	RPMs        []catalogRPM  `json:"rpms"`
}

type catalogStatus struct {
	Sailjail    string         `json:"sailjail"`
	Setuid      []catalogOwner `json:"setuid"`
	Permissions []string       `json:"permissions"`
	RedFlags    []catalogFlag  `json:"red_flags"`
}

type catalogOwner struct {
	Path string `json:"path"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
}

type catalogFlag struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
}

type catalogRPM struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Release  string   `json:"release"`
	Arch     string   `json:"arch"`
	Install  bool     `json:"install"`
	Requires []string `json:"requires"`
	SHA256   string   `json:"sha256,omitempty"`
	// Href is the path of the RPM inside the repository, such as
	// y/yottagram-0.2.9-1.aarch64.rpm. The name, version, and release
	// fields are not a reliable filename.
	Href string `json:"href,omitempty"`
}

// PublishCatalog reads every stored publisher mirror and the CDN application
// backup, then writes info.json.xz. Description, icon, and screenshots come
// from that backup when an application matches a package.
func PublishCatalog(ctx context.Context, store *s3store.Store) error {
	keys, err := store.List(ctx, statePrefix)
	if err != nil {
		return err
	}
	sort.Strings(keys)
	var sources []catalogSource
	seen := 0
	for _, key := range keys {
		if !strings.HasSuffix(key, ".json") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		body, ok, err := store.GetIfExists(ctx, key)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var st publisherState
		if err := json.Unmarshal(body, &st); err != nil {
			return fmt.Errorf("parse %s: %w", key, err)
		}
		if err := validPublisher(st.Publisher); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		seen++
		src, err := sourceFromState(ctx, store, st)
		if err != nil {
			return err
		}
		sources = append(sources, src)
		if seen%50 == 0 {
			log.Printf("catalog read %d publishers", seen)
		}
	}
	apps, err := loadAppIndex(ctx, store)
	if err != nil {
		return err
	}
	body, err := encodeCatalog(assembleCatalog(sources, apps))
	if err != nil {
		return err
	}
	res, err := store.PutBytes(ctx, catalogKey, "application/x-xz", body, nil)
	if err != nil {
		return err
	}
	if err := mergeManifest(ctx, store, []objectRec{{
		Path: catalogKey, Size: res.Size, SHA256: res.SHA256, Cache: "short",
	}}, "", nil); err != nil {
		return err
	}
	log.Printf("info %s (%d publishers, %d openrepos apps)", catalogPublic, seen, len(apps))
	return nil
}

func sourceFromState(ctx context.Context, store *s3store.Store, st publisherState) (catalogSource, error) {
	key, href, err := primaryObject(st)
	if err != nil {
		return catalogSource{}, err
	}
	tmp, err := os.CreateTemp("", "ftuna-primary-*")
	if err != nil {
		return catalogSource{}, err
	}
	name := tmp.Name()
	tmp.Close()
	defer os.Remove(name)
	if err := store.GetFile(ctx, key, name); err != nil {
		return catalogSource{}, fmt.Errorf("%s: %w", st.Publisher, err)
	}
	pkgs, err := parsePrimary(name, href)
	if err != nil {
		return catalogSource{}, fmt.Errorf("%s: %w", st.Publisher, err)
	}
	pkgs, err = applyObjectSHA256(st.Publisher, pkgs, st.Objects)
	if err != nil {
		return catalogSource{}, err
	}
	updated, err := time.Parse(time.RFC3339, st.SyncedAt)
	if err != nil {
		updated = time.Time{}
	}
	return catalogSource{Publisher: st.Publisher, Updated: updated.UTC(), RPMs: pkgs}, nil
}

func applyObjectSHA256(publisher string, pkgs []pkgRef, objects []objectRec) ([]pkgRef, error) {
	sums := map[string]string{}
	for _, obj := range objects {
		if obj.SHA256 != "" {
			sums[obj.Path] = strings.ToLower(obj.SHA256)
		}
	}
	for i := range pkgs {
		if pkgs[i].Name == "" || pkgs[i].Arch == "src" || pkgs[i].Arch == "nosrc" {
			continue
		}
		key := repoKey(publisher, pkgs[i].Href)
		sum := sums[key]
		if sum == "" {
			return nil, fmt.Errorf("%s: %s has no sha256", publisher, pkgs[i].Href)
		}
		pkgs[i].SHA256 = sum
	}
	return pkgs, nil
}

func primaryObject(st publisherState) (key, href string, err error) {
	prefix := publisherPrefix(st.Publisher) + "personal/main/"
	var hrefs []string
	for _, obj := range st.Objects {
		if !strings.HasPrefix(obj.Path, prefix) {
			continue
		}
		rel := strings.TrimPrefix(obj.Path, prefix)
		base := rel
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if strings.Contains(base, "primary.xml") && !strings.Contains(base, "sqlite") {
			hrefs = append(hrefs, rel)
		}
	}
	if len(hrefs) == 0 {
		return "", "", fmt.Errorf("%s: stored mirror has no primary.xml", st.Publisher)
	}
	sort.Strings(hrefs)
	href = hrefs[0]
	for _, candidate := range hrefs {
		if strings.HasSuffix(candidate, "primary.xml.gz") {
			href = candidate
			break
		}
	}
	return prefix + href, href, nil
}

func assembleCatalog(sources []catalogSource, apps map[string]appBackup) catalogDoc {
	doc := catalogDoc{
		Version:      1,
		Categories:   []string{"application", "library"},
		Repositories: []catalogRepo{},
		Packages:     []catalogPkg{},
	}
	repos := map[string]catalogRepo{}
	var newest time.Time
	for _, src := range sources {
		groups := map[string][]pkgRef{}
		var order []string
		for _, rpm := range src.RPMs {
			if rpm.Name == "" || rpm.Arch == "src" || rpm.Arch == "nosrc" {
				continue
			}
			id := packageID(src.Publisher, sourceName(rpm.Source, rpm.Name))
			if _, ok := groups[id]; !ok {
				order = append(order, id)
			}
			groups[id] = append(groups[id], rpm)
		}
		if len(order) == 0 {
			continue
		}
		repo := catalogRepo{ID: repoID(src.Publisher), URL: repoURL(src.Publisher)}
		repos[repo.ID] = repo
		for _, id := range order {
			meta, ok := matchApp(apps, src.Publisher, groups[id])
			pkg, when := packageOf(id, repo.ID, src.Updated, groups[id], meta, ok)
			doc.Packages = append(doc.Packages, pkg)
			if when.After(newest) {
				newest = when
			}
		}
	}
	if !newest.IsZero() {
		doc.Updated = newest.Format(time.RFC3339)
	}
	for _, repo := range repos {
		doc.Repositories = append(doc.Repositories, repo)
	}
	sort.Slice(doc.Repositories, func(i, j int) bool { return doc.Repositories[i].ID < doc.Repositories[j].ID })
	sort.Slice(doc.Packages, func(i, j int) bool {
		if doc.Packages[i].ID != doc.Packages[j].ID {
			return doc.Packages[i].ID < doc.Packages[j].ID
		}
		return doc.Packages[i].Repo < doc.Packages[j].Repo
	})
	return doc
}

func packageOf(id, repo string, fallback time.Time, rows []pkgRef, meta appBackup, hasMeta bool) (catalogPkg, time.Time) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		if rows[i].Arch != rows[j].Arch {
			return rows[i].Arch < rows[j].Arch
		}
		if rows[i].Version != rows[j].Version {
			return rows[i].Version < rows[j].Version
		}
		return rows[i].Release < rows[j].Release
	})
	app := false
	var newest time.Time
	sawBuild := false
	rpms := make([]catalogRPM, 0, len(rows))
	for _, row := range rows {
		if row.App {
			app = true
		}
		if row.Build > 0 {
			when := time.Unix(row.Build, 0).UTC()
			if !sawBuild || when.After(newest) {
				newest = when
			}
			sawBuild = true
		}
		rpms = append(rpms, catalogRPM{
			Name:     row.Name,
			Version:  row.Version,
			Release:  row.Release,
			Arch:     row.Arch,
			Install:  installable(row.Name),
			Requires: cleanRequires(row.Requires),
			SHA256:   row.SHA256,
			Href:     row.Href,
		})
	}
	summary, description := primaryText(rows)
	body, ai := chumAI(description)
	icon := ""
	shots := []string{}
	category := "library"
	if app {
		category = "application"
	}
	if hasMeta {
		if title := strings.TrimSpace(meta.Title); title != "" {
			summary = title
		}
		if plain := plainDescription(meta.Description); plain != "" {
			body = plain
		}
		if img := cdnImage(meta.Icon); img != "" {
			icon = img
			category = "application"
		}
		for _, shot := range meta.Screenshots {
			if img := cdnImage(shot); img != "" {
				shots = append(shots, img)
			}
		}
	}
	if !sawBuild {
		newest = fallback.UTC()
	}
	updated := ""
	if !newest.IsZero() {
		updated = newest.Format(time.RFC3339)
	}
	return catalogPkg{
		ID:          id,
		Category:    category,
		Updated:     updated,
		Summary:     summary,
		Description: aibadge.Append(body, ai),
		Icon:        icon,
		Screenshots: shots,
		Repo:        repo,
		Status: catalogStatus{
			Sailjail:    "unknown",
			Setuid:      []catalogOwner{},
			Permissions: []string{},
			RedFlags:    []catalogFlag{},
		},
		RPMs: rpms,
	}, newest
}

func primaryText(rows []pkgRef) (string, string) {
	for _, row := range rows {
		if installable(row.Name) && (row.Summary != "" || row.Description != "") {
			return row.Summary, row.Description
		}
	}
	if len(rows) == 0 {
		return "", ""
	}
	return rows[0].Summary, rows[0].Description
}

func installable(name string) bool {
	return !strings.HasSuffix(name, "-debuginfo") && !strings.HasSuffix(name, "-debugsource") && !strings.HasSuffix(name, "-doc")
}

func cleanRequires(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, spec := range in {
		name := strings.TrimSpace(spec)
		if name == "" || strings.HasPrefix(name, "(") || strings.HasPrefix(name, "rpmlib(") {
			continue
		}
		if i := strings.IndexAny(name, " \t"); i >= 0 {
			name = name[:i]
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func packageID(publisher, source string) string {
	return "openrepos.net/" + publisher + "/" + source
}

func repoID(publisher string) string {
	return "openrepos-" + publisher
}

func repoURL(publisher string) string {
	return "https://repo-openrepos.ftuna.eu/" + url.PathEscape(publisher) + "/personal/main/"
}

func sourceName(sourcerpm, name string) string {
	base := strings.TrimSpace(sourcerpm)
	if strings.HasSuffix(base, ".src.rpm") {
		base = strings.TrimSuffix(base, ".src.rpm")
		rel := strings.LastIndex(base, "-")
		ver := -1
		if rel > 0 {
			ver = strings.LastIndex(base[:rel], "-")
		}
		if ver > 0 {
			return base[:ver]
		}
	}
	for _, suf := range []string{"-debuginfo", "-debugsource", "-doc"} {
		if strings.HasSuffix(name, suf) {
			return strings.TrimSuffix(name, suf)
		}
	}
	return name
}

func encodeCatalog(doc catalogDoc) ([]byte, error) {
	return xzjson.Encode(doc)
}
