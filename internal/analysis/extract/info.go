package extract

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/ulikunitz/xz"
)

// catalogSums maps a repository path to the sha256 in info.json.xz.
// The path is the catalog href. A name, version, and release are not a path.
type catalogSums map[string]string

type infoDoc struct {
	Repositories []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"repositories"`
	Packages []struct {
		Repo string `json:"repo"`
		RPMs []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Release string `json:"release"`
			Arch    string `json:"arch"`
			SHA256  string `json:"sha256"`
			Href    string `json:"href"`
		} `json:"rpms"`
	} `json:"packages"`
}

func readInfo(ctx context.Context, client *http.Client, root string) (catalogSums, error) {
	doc, err := fetchInfo(ctx, client, root)
	if doc == nil || err != nil {
		return nil, err
	}
	return indexInfo(*doc)
}

func fetchInfo(ctx context.Context, client *http.Client, root string) (*infoDoc, error) {
	rawURL := strings.TrimRight(root, "/") + "/info.json.xz"
	body, err := get(ctx, client, rawURL, maxMeta)
	if errors.Is(err, errNotFound) {
		log.Printf("info.json.xz: not published")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := xz.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("info.json.xz: %w", err)
	}
	plain, err := io.ReadAll(io.LimitReader(r, maxPlain+1))
	if err != nil {
		return nil, fmt.Errorf("info.json.xz: %w", err)
	}
	if int64(len(plain)) > maxPlain {
		return nil, fmt.Errorf("info.json.xz: larger than %d bytes", maxPlain)
	}
	var doc infoDoc
	if err := json.Unmarshal(plain, &doc); err != nil {
		return nil, fmt.Errorf("info.json.xz: %w", err)
	}
	return &doc, nil
}

// openreposFromInfo lists binary RPMs from the catalog. rpm-md is not read.
// An RPM with no href keeps Repo and its NEVRA so the path can be resolved
// later, and only for a package that still needs to be downloaded.
func openreposFromInfo(root string, doc infoDoc) ([]Package, error) {
	repos := map[string]string{}
	for _, repo := range doc.Repositories {
		if strings.TrimSpace(repo.ID) == "" {
			return nil, fmt.Errorf("info.json.xz: repository has no id")
		}
		prefix, err := repoPathPrefix(repo.URL)
		if err != nil {
			return nil, fmt.Errorf("info.json.xz: repository %s: %w", repo.ID, err)
		}
		if _, err := openreposPublisher(prefix); err != nil {
			return nil, fmt.Errorf("info.json.xz: repository %s: %w", repo.ID, err)
		}
		if prev, ok := repos[repo.ID]; ok && prev != prefix {
			return nil, fmt.Errorf("info.json.xz: repository %s has two urls", repo.ID)
		}
		repos[repo.ID] = prefix
	}
	var out []Package
	seen := map[string]string{}
	for _, pkg := range doc.Packages {
		prefix, ok := repos[pkg.Repo]
		if !ok {
			return nil, fmt.Errorf("info.json.xz: package repo %q is not listed", pkg.Repo)
		}
		publisher, err := openreposPublisher(prefix)
		if err != nil {
			return nil, err
		}
		repoURL := strings.TrimRight(root, "/") + prefix
		for _, rpm := range pkg.RPMs {
			sha, err := parseSHA256(rpm.SHA256)
			if err != nil {
				return nil, fmt.Errorf("info.json.xz: %s: %w", rpm.Name, err)
			}
			item := Package{
				Sum:     sha,
				SumType: "sha256",
				Repo:    repoURL,
				Name:    rpm.Name,
				Version: rpm.Version,
				Release: rpm.Release,
				Arch:    rpm.Arch,
			}
			// The catalog path is the file. The same path cannot be two hashes.
			// Without a path, the hash is the identity. The header name is not.
			id := publisher + "\n" + sha
			href := strings.TrimSpace(rpm.Href)
			if href != "" {
				href, err = safeRel(href)
				if err != nil {
					return nil, fmt.Errorf("info.json.xz: %s: %w", rpm.Name, err)
				}
				item.Key = "mirror/openrepos/" + publisher + "/personal/main/" + href
				item.URL, err = joinRepo(repoURL, href)
				if err != nil {
					return nil, fmt.Errorf("info.json.xz: %s: %w", href, err)
				}
				id = publisher + "\n" + href
			} else {
				item.Key = "mirror/openrepos/" + publisher + "/" + sha
			}
			if prev, ok := seen[id]; ok {
				if prev != sha {
					return nil, fmt.Errorf("info.json.xz: %s has sha256 %s and %s", item.Key, prev, sha)
				}
				continue
			}
			seen[id] = sha
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: info.json.xz lists no packages", root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	log.Printf("openrepos: info.json.xz: %d packages", len(out))
	return out, nil
}

func openreposPublisher(prefix string) (string, error) {
	parts := strings.Split(strings.Trim(prefix, "/"), "/")
	if len(parts) != 3 || parts[1] != "personal" || parts[2] != "main" || parts[0] == "" || strings.Contains(parts[0], "/") {
		return "", fmt.Errorf("unexpected repository path %q", prefix)
	}
	return parts[0], nil
}

func indexInfo(doc infoDoc) (catalogSums, error) {
	repos := map[string]string{}
	for _, repo := range doc.Repositories {
		if strings.TrimSpace(repo.ID) == "" {
			return nil, fmt.Errorf("info.json.xz: repository has no id")
		}
		prefix, err := repoPathPrefix(repo.URL)
		if err != nil {
			return nil, fmt.Errorf("info.json.xz: repository %s: %w", repo.ID, err)
		}
		if prev, ok := repos[repo.ID]; ok && prev != prefix {
			return nil, fmt.Errorf("info.json.xz: repository %s has two urls", repo.ID)
		}
		repos[repo.ID] = prefix
	}
	out := catalogSums{}
	missing := 0
	for _, pkg := range doc.Packages {
		prefix, ok := repos[pkg.Repo]
		if !ok {
			return nil, fmt.Errorf("info.json.xz: package repo %q is not listed", pkg.Repo)
		}
		for _, rpm := range pkg.RPMs {
			if strings.TrimSpace(rpm.SHA256) == "" {
				continue
			}
			sha, err := parseSHA256(rpm.SHA256)
			if err != nil {
				return nil, fmt.Errorf("info.json.xz: %s: %w", rpm.Name, err)
			}
			href := strings.TrimSpace(rpm.Href)
			if href == "" {
				missing++
				continue
			}
			href, err = safeRel(href)
			if err != nil {
				return nil, fmt.Errorf("info.json.xz: %s: %w", rpm.Name, err)
			}
			full := prefix + href
			if prev, ok := out[full]; ok && prev != sha {
				return nil, fmt.Errorf("info.json.xz: %s has sha256 %s and %s", full, prev, sha)
			}
			out[full] = sha
		}
	}
	if missing > 0 {
		log.Printf("info.json.xz: %d packages have no path", missing)
	}
	return out, nil
}

func overlayInfo(sums catalogSums, pkgs []Package) ([]Package, error) {
	if sums == nil {
		return pkgs, nil
	}
	out := append([]Package(nil), pkgs...)
	matched := 0
	for i := range out {
		pkgPath, err := packagePath(out[i].URL)
		if err != nil {
			return nil, err
		}
		sha, ok := sums[pkgPath]
		if !ok {
			continue
		}
		out[i].Sum = sha
		out[i].SumType = "sha256"
		matched++
	}
	log.Printf("info.json.xz: sha256 for %d/%d packages", matched, len(out))
	return out, nil
}

func repoPathPrefix(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path == "" {
		return "", fmt.Errorf("bad repository url %q", raw)
	}
	p := path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
	if p == "/" || p == "." {
		return "", fmt.Errorf("repository url %q has no path", raw)
	}
	return p + "/", nil
}

func packagePath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", fmt.Errorf("bad rpm url %q", raw)
	}
	p := path.Clean(u.Path)
	if p == "/" || p == "." || !strings.HasSuffix(strings.ToLower(p), ".rpm") {
		return "", fmt.Errorf("bad rpm url %q", raw)
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p, nil
}

func parseSHA256(raw string) (string, error) {
	sum := strings.ToLower(strings.TrimSpace(raw))
	if len(sum) != 64 {
		return "", fmt.Errorf("bad sha256 %q", raw)
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", fmt.Errorf("bad sha256 %q", raw)
	}
	return sum, nil
}
