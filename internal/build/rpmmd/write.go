// Package rpmmd reads RPM headers and writes gzip rpm-md repositories.
package rpmmd

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Write walks RPMs under dir and writes dir/repodata. Existing repodata is replaced.
// The gzip names are the sha256 of the compressed metadata.
func Write(dir string) error {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && d.Name() == "repodata" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".rpm") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	pkgs := make([]Package, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		pkg, err := Inspect(path, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		pkgs = append(pkgs, pkg)
	}
	return WritePackages(dir, pkgs)
}

// WritePackages writes dir/repodata from package fragments already in hand.
func WritePackages(dir string, pkgs []Package) error {
	if len(pkgs) == 0 {
		return fmt.Errorf("no rpms in %s", dir)
	}
	sorted := append([]Package(nil), pkgs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Href < sorted[j].Href })
	seen := map[string]bool{}
	for _, pkg := range sorted {
		if pkg.Href == "" || pkg.Primary == "" || pkg.Filelists == "" || pkg.Other == "" {
			return fmt.Errorf("incomplete package %q", pkg.Href)
		}
		if seen[pkg.Href] {
			return fmt.Errorf("duplicate location %s", pkg.Href)
		}
		seen[pkg.Href] = true
	}
	repo := filepath.Join(dir, "repodata")
	if err := os.RemoveAll(repo); err != nil {
		return err
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	revision := time.Now().Unix()
	kinds := []struct {
		name string
		body []byte
	}{
		{"primary", wrap("metadata", nsCommon, `xmlns:rpm="`+nsRPM+`"`, sorted, func(p Package) string { return p.Primary })},
		{"filelists", wrap("filelists", nsFilelists, "", sorted, func(p Package) string { return p.Filelists })},
		{"other", wrap("otherdata", nsOther, "", sorted, func(p Package) string { return p.Other })},
	}
	var files []metaFile
	for _, kind := range kinds {
		file, err := writeGzip(repo, kind.name, kind.body)
		if err != nil {
			return err
		}
		files = append(files, file)
	}
	body := repomdXML(files, revision)
	return os.WriteFile(filepath.Join(repo, "repomd.xml"), body, 0o644)
}

func writeGzip(dir, kind string, plain []byte) (metaFile, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		return metaFile{}, err
	}
	if err := zw.Close(); err != nil {
		return metaFile{}, err
	}
	gz := buf.Bytes()
	sum := sha256.Sum256(gz)
	open := sha256.Sum256(plain)
	name := hex.EncodeToString(sum[:]) + "-" + kind + ".xml.gz"
	if err := os.WriteFile(filepath.Join(dir, name), gz, 0o644); err != nil {
		return metaFile{}, err
	}
	return metaFile{
		Type:         kind,
		Name:         name,
		Checksum:     hex.EncodeToString(sum[:]),
		OpenChecksum: hex.EncodeToString(open[:]),
		Size:         int64(len(gz)),
		OpenSize:     int64(len(plain)),
	}, nil
}

// Gunzip decompresses a gzip payload.
func Gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
