package extract

import (
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"path"
	"strings"
)

type repoData struct {
	Type         string
	Href         string
	Checksum     string
	ChecksumType string
}

type pkgRef struct {
	Name     string
	Version  string
	Release  string
	Arch     string
	Href     string
	Checksum string
	SumType  string
	Size     int64
}

func readRepo(ctx context.Context, client *http.Client, repoURL string) ([]pkgRef, error) {
	repoURL = strings.TrimRight(repoURL, "/") + "/"
	raw, err := get(ctx, client, repoURL+"repodata/repomd.xml", maxMeta)
	if err != nil {
		return nil, err
	}
	md, err := parseRepoMD(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repoURL, err)
	}
	primary, err := primaryData(md)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repoURL, err)
	}
	fileURL, err := joinRepo(repoURL, primary.Href)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repoURL, err)
	}
	body, err := get(ctx, client, fileURL, maxMeta)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("%s: primary metadata not found", fileURL)
		}
		return nil, err
	}
	if err := Verify(primary.ChecksumType, primary.Checksum, body); err != nil {
		return nil, fmt.Errorf("%s: %w", fileURL, err)
	}
	plain, err := decompress(primary.Href, body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fileURL, err)
	}
	pkgs, err := parsePrimary(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fileURL, err)
	}
	return pkgs, nil
}

func parseRepoMD(b []byte) ([]repoData, error) {
	var doc struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			Checksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
		} `xml:"data"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Data) == 0 {
		return nil, fmt.Errorf("repomd has no data entries")
	}
	var out []repoData
	for _, d := range doc.Data {
		href, err := safeRel(strings.TrimSpace(d.Location.Href))
		if err != nil {
			return nil, fmt.Errorf("repomd %s: %w", d.Type, err)
		}
		out = append(out, repoData{
			Type:         d.Type,
			Href:         href,
			Checksum:     strings.TrimSpace(d.Checksum.Value),
			ChecksumType: strings.TrimSpace(d.Checksum.Type),
		})
	}
	return out, nil
}

func primaryData(md []repoData) (repoData, error) {
	var found *repoData
	for i := range md {
		if md[i].Type != "primary" {
			continue
		}
		if strings.HasSuffix(md[i].Href, ".xml.gz") {
			found = &md[i]
			break
		}
		if found == nil {
			found = &md[i]
		}
	}
	if found == nil {
		return repoData{}, fmt.Errorf("repomd has no primary metadata")
	}
	if found.Checksum == "" || found.ChecksumType == "" {
		return repoData{}, fmt.Errorf("primary metadata has no checksum")
	}
	return *found, nil
}

func parsePrimary(r io.Reader) ([]pkgRef, error) {
	dec := xml.NewDecoder(r)
	var pkgs []pkgRef
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return pkgs, nil
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "package" {
			continue
		}
		var p struct {
			Name    string `xml:"name"`
			Arch    string `xml:"arch"`
			Version struct {
				Ver string `xml:"ver,attr"`
				Rel string `xml:"rel,attr"`
			} `xml:"version"`
			Checksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			Size struct {
				Package int64 `xml:"package,attr"`
			} `xml:"size"`
		}
		if err := dec.DecodeElement(&p, &se); err != nil {
			return nil, err
		}
		href, err := safeRel(strings.TrimSpace(p.Location.Href))
		if err != nil {
			return nil, fmt.Errorf("package %s: %w", p.Name, err)
		}
		pkgs = append(pkgs, pkgRef{
			Name:     strings.TrimSpace(p.Name),
			Version:  strings.TrimSpace(p.Version.Ver),
			Release:  strings.TrimSpace(p.Version.Rel),
			Arch:     strings.TrimSpace(p.Arch),
			Href:     href,
			Checksum: strings.TrimSpace(p.Checksum.Value),
			SumType:  strings.TrimSpace(p.Checksum.Type),
			Size:     p.Size.Package,
		})
	}
}

func decompress(name string, body []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(name, ".gz"):
		r, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readPlain(r)
	case strings.HasSuffix(name, ".bz2"):
		return readPlain(bzip2.NewReader(bytes.NewReader(body)))
	case strings.HasSuffix(name, ".xml"):
		return readPlain(bytes.NewReader(body))
	default:
		return nil, fmt.Errorf("unsupported metadata compression for %s", name)
	}
}

func readPlain(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxPlain+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxPlain {
		return nil, fmt.Errorf("metadata larger than %d bytes", maxPlain)
	}
	return body, nil
}

// Verify checks body against a repomd or primary checksum.
// sumType is sha, sha1, or sha256.
func Verify(sumType, want string, body []byte) error {
	got, err := hashBytes(sumType, body)
	if err != nil {
		return err
	}
	kind, err := normSum(sumType)
	if err != nil {
		return err
	}
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return fmt.Errorf("missing %s checksum", kind)
	}
	if got != want {
		return fmt.Errorf("%s checksum %s, metadata says %s", kind, got, want)
	}
	return nil
}

func hashBytes(sumType string, body []byte) (string, error) {
	kind, err := normSum(sumType)
	if err != nil {
		return "", err
	}
	var h hash.Hash
	switch kind {
	case "sha1":
		h = sha1.New()
	default:
		h = sha256.New()
	}
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func normSum(sumType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(sumType)) {
	case "sha", "sha1":
		return "sha1", nil
	case "sha256":
		return "sha256", nil
	default:
		return "", fmt.Errorf("unsupported checksum type %q", sumType)
	}
}

func safeRel(href string) (string, error) {
	if href == "" {
		return "", fmt.Errorf("empty location")
	}
	if strings.ContainsRune(href, 0) || strings.ContainsRune(href, '\\') {
		return "", fmt.Errorf("location %q contains a forbidden character", href)
	}
	if strings.Contains(href, "://") || strings.HasPrefix(href, "/") || strings.ContainsAny(href, "?#") {
		return "", fmt.Errorf("location %q is not a relative repository path", href)
	}
	if path.Clean(href) != href || href == "." || href == ".." || strings.HasPrefix(href, "../") {
		return "", fmt.Errorf("location %q is not a clean relative path", href)
	}
	return href, nil
}
