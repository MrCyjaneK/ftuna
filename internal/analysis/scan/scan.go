// Package scan reads binary RPMs from Chum, the OpenRepos mirror, and the
// FTuna oss repository, and writes one analysis result per package.
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
	"github.com/mrcyjanek/ftuna/internal/analysis/extract"
	"github.com/mrcyjanek/ftuna/internal/analysis/ftuna"
	"github.com/mrcyjanek/ftuna/internal/analysis/rpmfile"
)

// Run reads each configured source, analyzes each binary RPM, and stores the
// result under cfg.RatingsDir. The first error stops the scan.
func Run(ctx context.Context, cfg Config) error {
	if len(cfg.Sources) == 0 {
		cfg.Sources = []string{extract.Chum, extract.OpenRepos, extract.OSS}
	}
	if strings.TrimSpace(cfg.RatingsDir) == "" {
		return fmt.Errorf("ratings directory is required")
	}
	client := &http.Client{
		Transport: &http.Transport{DisableCompression: true},
	}
	var pkgs []extract.Package
	seen := map[string]bool{}
	for _, name := range cfg.Sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		found, err := extract.Collect(ctx, client, name, cfg.root(name))
		if err != nil {
			return err
		}
		for _, pkg := range found {
			if pkg.Key != "" && seen["key:"+pkg.Key] {
				continue
			}
			if pkg.URL != "" && seen["url:"+pkg.URL] {
				continue
			}
			if pkg.Key != "" {
				seen["key:"+pkg.Key] = true
			}
			if pkg.URL != "" {
				seen["url:"+pkg.URL] = true
			}
			pkgs = append(pkgs, pkg)
		}
		log.Printf("%s: %d packages", name, len(found))
	}
	cacheDir, err := rpmCacheDir(cfg.CacheDir)
	if err != nil {
		return err
	}
	cache := &extract.RepoCache{}
	for i, pkg := range pkgs {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Printf("%d/%d %s", i+1, len(pkgs), pkg.Key)
		if err := one(ctx, client, cache, cfg.RatingsDir, cacheDir, pkg); err != nil {
			return fmt.Errorf("%s: %w", pkg.Key, err)
		}
	}
	return nil
}

func one(ctx context.Context, client *http.Client, cache *extract.RepoCache, ratingsDir, cacheDir string, pkg extract.Package) error {
	if pkg.SumType != "sha256" {
		return fmt.Errorf("refusing to download without a sha256")
	}
	exists, err := resultExists(ratingsDir, pkg.Sum)
	if err != nil {
		return err
	}
	if exists {
		log.Printf("skip %s", pkg.Key)
		return nil
	}
	if pkg.URL == "" {
		pkg, err = cache.Fill(ctx, client, pkg)
		if err != nil {
			return err
		}
	}
	urls := pkg.URLs
	if pkg.URL != "" {
		urls = []string{pkg.URL}
	}
	if len(urls) == 0 {
		return fmt.Errorf("no download url")
	}
	if body, ok := readCachedRPM(cacheDir, pkg.Sum); ok {
		pkg, err = locate(pkg, urls[0])
		if err != nil {
			return err
		}
		log.Printf("cache %s", pkg.Key)
		return finish(ratingsDir, pkg, body)
	}
	var body []byte
	var matched string
	for _, raw := range urls {
		got, err := fetchRPM(ctx, client, raw)
		if err != nil {
			return err
		}
		if len(urls) == 1 && pkg.Size > 0 && int64(len(got)) != pkg.Size {
			return fmt.Errorf("size %d, metadata says %d", len(got), pkg.Size)
		}
		if err := extract.Verify(pkg.SumType, pkg.Sum, got); err != nil {
			if len(urls) == 1 {
				return err
			}
			continue
		}
		body = got
		matched = raw
		break
	}
	if body == nil {
		return fmt.Errorf("no file matches sha256 %s", pkg.Sum)
	}
	pkg, err = locate(pkg, matched)
	if err != nil {
		return err
	}
	if err := writeCachedRPM(cacheDir, pkg.Sum, body); err != nil {
		log.Printf("rpm cache: %s", err)
	}
	return finish(ratingsDir, pkg, body)
}

func locate(pkg extract.Package, matched string) (extract.Package, error) {
	if pkg.URL != "" || len(pkg.URLs) <= 1 {
		return pkg, nil
	}
	u, err := url.Parse(matched)
	if err != nil || u.Path == "" {
		return pkg, fmt.Errorf("bad rpm url %q", matched)
	}
	pkg.Key = "mirror/openrepos" + u.Path
	return pkg, nil
}

func finish(ratingsDir string, pkg extract.Package, body []byte) error {
	doc, err := Analyze(pkg.Key, pkg.Sum, body)
	if err != nil {
		return err
	}
	raw, err := check.Encode(doc)
	if err != nil {
		return err
	}
	dest, err := writeResult(ratingsDir, pkg.Sum, raw)
	if err != nil {
		return err
	}
	log.Printf("wrote %s", dest)
	return nil
}

func resultPath(dir, sha string) (string, error) {
	if len(sha) < 6 || strings.ContainsAny(sha, `/\`) {
		return "", fmt.Errorf("bad sha256 %q", sha)
	}
	return filepath.Join(dir, filepath.FromSlash(check.ObjectKey(sha))), nil
}

func resultExists(dir, sha string) (bool, error) {
	path, err := resultPath(dir, sha)
	if err != nil {
		return false, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var doc struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.Version != check.Version {
		return false, nil
	}
	return true, nil
}

func writeResult(dir, sha string, body []byte) (string, error) {
	path, err := resultPath(dir, sha)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func fetchRPM(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("bad rpm url %q", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "FTunaAnalysis/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s: %s", raw, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Analyze runs the registered modules on one RPM. A package that cannot be
// read is still a result: the read error is stored as an issue.
func Analyze(key, sha string, body []byte) (check.Result, error) {
	id, files, err := rpmfile.Read(body)
	doc := check.Result{
		Version:    check.Version,
		SHA256:     sha,
		Key:        key,
		Name:       id.Name,
		RPMVersion: id.Version,
		Release:    id.Release,
		Arch:       id.Arch,
	}
	if err != nil {
		log.Printf("%s: %s", key, err)
		doc.Issues = []check.Issue{{
			Module: "rpm",
			Path:   key,
			Code:   "rpm.read",
			Detail: err.Error(),
			Color:  check.ColorGray,
		}}
		doc.Issues = append(doc.Issues, ftuna.Check(key, ftuna.RecipesDir())...)
		return doc, nil
	}
	doc.Issues = check.Apply(files)
	doc.Issues = append(doc.Issues, ftuna.Check(key, ftuna.RecipesDir())...)
	return doc, nil
}
