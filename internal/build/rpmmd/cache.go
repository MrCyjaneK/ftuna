package rpmmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CacheDir is ~/.cache/ftuna/oss/<version>/<arch>, or
// $XDG_CACHE_HOME/ftuna/oss/<version>/<arch> when that variable is set.
func CacheDir(version, arch string) (string, error) {
	if !cacheSeg(version) || !cacheSeg(arch) {
		return "", fmt.Errorf("bad cache path %s/%s", version, arch)
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "ftuna", "oss", version, arch), nil
}

func cacheSeg(s string) bool {
	if s == "" || s == "." || s == ".." || strings.Contains(s, "/") || strings.Contains(s, "\\") {
		return false
	}
	return true
}

// LoadPackage reads the cached fragments for href. ok is false when the file is absent.
func LoadPackage(dir, href string) (Package, bool, error) {
	path, err := cachePath(dir, href)
	if err != nil {
		return Package{}, false, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Package{}, false, nil
		}
		return Package{}, false, err
	}
	var pkg Package
	if err := json.Unmarshal(body, &pkg); err != nil {
		return Package{}, false, fmt.Errorf("%s: %w", path, err)
	}
	pkg.Href = href
	if pkg.Primary == "" || pkg.Filelists == "" || pkg.Other == "" {
		return Package{}, false, fmt.Errorf("%s: incomplete cache entry", href)
	}
	return pkg, true, nil
}

// SavePackage writes the fragments for pkg.Href under dir.
func SavePackage(dir string, pkg Package) error {
	path, err := cachePath(dir, pkg.Href)
	if err != nil {
		return err
	}
	if pkg.Primary == "" || pkg.Filelists == "" || pkg.Other == "" || pkg.SHA256 == "" {
		return fmt.Errorf("%s: incomplete package", pkg.Href)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(pkg)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

// Prune removes cached packages whose location is not in keep.
func Prune(dir string, keep map[string]bool) error {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		href := strings.TrimSuffix(filepath.ToSlash(rel), ".json")
		if keep[href] {
			return nil
		}
		return os.Remove(path)
	})
}

func cachePath(dir, href string) (string, error) {
	href, err := cleanHref(href)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.FromSlash(href)+".json"), nil
}
