package extract

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// RepoCache remembers primary metadata for repositories whose catalog entry
// has a sha256 but no RPM path. A repository is read only when a package
// from it still has to be downloaded.
type RepoCache struct {
	mu    sync.Mutex
	repos map[string][]pkgRef
}

// Fill sets URL, Key, and Size from rpm-md when the catalog did not include
// the RPM path. A package that already has a URL is returned unchanged.
func (c *RepoCache) Fill(ctx context.Context, client *http.Client, pkg Package) (Package, error) {
	if pkg.URL != "" {
		return pkg, nil
	}
	if c == nil || pkg.Repo == "" || pkg.Name == "" || pkg.Version == "" || pkg.Release == "" || pkg.Arch == "" {
		return pkg, fmt.Errorf("no download url")
	}
	refs, err := c.primary(ctx, client, pkg.Repo)
	if err != nil {
		return pkg, err
	}
	var found []pkgRef
	for _, p := range refs {
		if !wantBinary(p) || p.Name != pkg.Name || p.Version != pkg.Version || p.Release != pkg.Release || p.Arch != pkg.Arch {
			continue
		}
		found = append(found, p)
	}
	if len(found) == 0 {
		return pkg, fmt.Errorf("%s-%s-%s.%s is not in primary metadata", pkg.Name, pkg.Version, pkg.Release, pkg.Arch)
	}
	prefix, err := repoPathPrefix(pkg.Repo)
	if err != nil {
		return pkg, err
	}
	if len(found) == 1 {
		pkg.URL, err = joinRepo(pkg.Repo, found[0].Href)
		if err != nil {
			return pkg, err
		}
		pkg.Key = "mirror/openrepos" + prefix + found[0].Href
		pkg.Size = found[0].Size
		return pkg, nil
	}
	pkg.URLs = nil
	for _, p := range found {
		raw, err := joinRepo(pkg.Repo, p.Href)
		if err != nil {
			return pkg, err
		}
		pkg.URLs = append(pkg.URLs, raw)
	}
	return pkg, nil
}

func (c *RepoCache) primary(ctx context.Context, client *http.Client, repo string) ([]pkgRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.repos == nil {
		c.repos = map[string][]pkgRef{}
	}
	if refs, ok := c.repos[repo]; ok {
		return refs, nil
	}
	refs, err := readRepo(ctx, client, repo)
	if err != nil {
		return nil, err
	}
	c.repos[repo] = refs
	return refs, nil
}
