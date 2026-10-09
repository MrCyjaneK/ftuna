// Package extract reads binary RPM lists from Chum, the OpenRepos mirror,
// and the FTuna oss repository. OpenRepos reads its list from info.json.xz
// when that catalog is published. Chum and oss read rpm-md. oss uses
// sha256 from info.json.xz when the catalog lists that RPM.
package extract

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	Chum      = "chum"
	OpenRepos = "openrepos"
	OSS       = "oss"

	ChumRoot      = "https://repo.sailfishos.org/obs/sailfishos:/chum/"
	OpenReposRoot = "https://repo-openrepos.ftuna.eu/"
	OSSRoot       = "https://repo-oss.ftuna.eu/"
)

// Package is one binary RPM published by a repository.
// Key is the stable name stored in the analysis result.
// URL is where the RPM bytes are downloaded from. It is empty when the
// catalog lists the RPM but not its repository path; Repo and the NEVRA
// fields then identify it so the path can be read from rpm-md.
type Package struct {
	Key     string
	URL     string
	Sum     string
	SumType string
	Size    int64
	Repo    string
	Name    string
	Version string
	Release string
	Arch    string
	// URLs are repository locations that share this RPM header. The catalog
	// did not include a path, and more than one file has this name, version,
	// release, and arch. The file is the location whose bytes match Sum.
	URLs []string
}

// Collect reads one source. root is the repository index; an empty root
// selects that source's public URL. name is chum, openrepos, or oss.
func Collect(ctx context.Context, client *http.Client, name, root string) ([]Package, error) {
	if strings.TrimSpace(root) == "" {
		switch name {
		case Chum:
			root = ChumRoot
		case OpenRepos:
			root = OpenReposRoot
		case OSS:
			root = OSSRoot
		default:
			return nil, fmt.Errorf("unknown source %q", name)
		}
	}
	root = strings.TrimRight(root, "/") + "/"
	switch name {
	case Chum:
		return chum(ctx, client, root)
	case OpenRepos:
		return openrepos(ctx, client, root)
	case OSS:
		return oss(ctx, client, root)
	default:
		return nil, fmt.Errorf("unknown source %q", name)
	}
}

func joinDir(base, name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return "", fmt.Errorf("bad directory %q", name)
	}
	return strings.TrimRight(base, "/") + "/" + url.PathEscape(name) + "/", nil
}

func joinRepo(base, rel string) (string, error) {
	rel, err := safeRel(rel)
	if err != nil {
		return "", err
	}
	base = strings.TrimRight(base, "/") + "/"
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return base + strings.Join(parts, "/"), nil
}

func wantBinary(p pkgRef) bool {
	return strings.HasSuffix(p.Href, ".rpm") && !strings.HasSuffix(p.Href, ".src.rpm") && p.Arch != "src"
}

func toPackages(repoURL, keyPrefix string, pkgs []pkgRef) ([]Package, error) {
	var out []Package
	for _, p := range pkgs {
		if !wantBinary(p) {
			continue
		}
		raw, err := joinRepo(repoURL, p.Href)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		kind, err := normSum(p.SumType)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Href, err)
		}
		if strings.TrimSpace(p.Checksum) == "" {
			return nil, fmt.Errorf("%s: missing checksum", p.Href)
		}
		out = append(out, Package{
			Key:     keyPrefix + p.Href,
			URL:     raw,
			Sum:     strings.ToLower(strings.TrimSpace(p.Checksum)),
			SumType: kind,
			Size:    p.Size,
		})
	}
	return out, nil
}
