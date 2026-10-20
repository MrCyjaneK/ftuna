// Package sfosbuild runs the sfosbuild command in a prepared tree.
package sfosbuild

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// logTailLimit is how much sfosbuild output is kept for status.json.
const logTailLimit = 256 * 1024

func Run(ctx context.Context, version, arches, project, outDir, buildPath string) (string, error) {
	bin, err := exec.LookPath("sfosbuild")
	if err != nil {
		return "", fmt.Errorf("sfosbuild not on PATH")
	}
	if strings.TrimSpace(buildPath) == "" {
		return "", fmt.Errorf("sfosbuild needs the directory that contains rpm/")
	}
	pkgs, err := packageFiles(project, buildPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	tail := &tailWriter{limit: logTailLimit}
	for _, pkg := range pkgs {
		// Flags have to come before the version. sfosbuild stops parsing at the first positional argument.
		cmd := exec.CommandContext(ctx, bin, "-o", outDir, version, arches, pkg)
		cmd.Dir = project
		// Stdout and stderr are written from different goroutines.
		cmd.Stdout = io.MultiWriter(os.Stdout, tail)
		cmd.Stderr = io.MultiWriter(os.Stderr, tail)
		if err := cmd.Run(); err != nil {
			return tail.String(), fmt.Errorf("sfosbuild %s %s %s: %w", version, arches, pkg, err)
		}
	}
	return tail.String(), nil
}

// packageFiles returns the yaml or spec files sfosbuild builds, relative to project.
// buildPath is the directory that contains rpm/. The first non-empty pattern wins.
func packageFiles(project, buildPath string) ([]string, error) {
	root := project
	if buildPath != "." {
		root = filepath.Join(project, filepath.FromSlash(buildPath))
	}
	for _, pattern := range []string{
		filepath.Join(root, "rpm", "*.yaml"),
		filepath.Join(root, "*.yaml"),
		filepath.Join(root, "rpm", "*.spec"),
		filepath.Join(root, "*.spec"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			continue
		}
		sort.Strings(matches)
		out := make([]string, 0, len(matches))
		for _, match := range matches {
			rel, err := filepath.Rel(project, match)
			if err != nil {
				return nil, err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return out, nil
	}
	return nil, fmt.Errorf("no rpm/*.yaml or rpm/*.spec in %s", buildPath)
}

// tailWriter keeps the last limit bytes written to it.
type tailWriter struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= w.limit {
		w.buf = append([]byte(nil), p[len(p)-w.limit:]...)
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.limit:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}
