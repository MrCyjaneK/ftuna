// Package checkout clones a pinned commit, applies patches, and commits the overlay.
package checkout

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CacheDir is ~/.cache/ftuna/src/<id>, or $XDG_CACHE_HOME/ftuna/src/<id>.
// The last element stays unique per recipe so sfosbuild image names do not collide.
func CacheDir(id string) (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "ftuna", "src", strings.ReplaceAll(id, "/", "_")), nil
}

func Prepare(ctx context.Context, url, commit, patchesDir, overlayDir, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		log.Printf("clone %s", url)
		if _, err := git(ctx, "", "clone", url, dest); err != nil {
			return err
		}
	}
	if _, err := git(ctx, dest, "reset", "--hard"); err != nil {
		return err
	}
	log.Printf("checkout %s", commit)
	if _, err := git(ctx, dest, "checkout", commit); err != nil {
		if _, ferr := git(ctx, dest, "fetch", "origin", commit); ferr != nil {
			return fmt.Errorf("checkout %s: %w", commit, err)
		}
		if _, err2 := git(ctx, dest, "checkout", commit); err2 != nil {
			return fmt.Errorf("checkout %s: %w", commit, err2)
		}
	}
	log.Printf("submodule update")
	if _, err := git(ctx, dest, "submodule", "update", "--init", "--force", "--recursive"); err != nil {
		return err
	}
	if err := applyPatches(ctx, dest, patchesDir); err != nil {
		return err
	}
	if err := applyOverlay(ctx, dest, overlayDir); err != nil {
		return err
	}
	return ensureSfosbuildGitkeep(ctx, dest)
}

func ensureSfosbuildGitkeep(ctx context.Context, dir string) error {
	sfosbuildDir := filepath.Join(dir, ".sfosbuild")
	_, err := os.Stat(sfosbuildDir)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	log.Printf(".sfosbuild/.gitkeep")
	gitkeep := filepath.Join(sfosbuildDir, ".gitkeep")
	if err := os.MkdirAll(sfosbuildDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(gitkeep, nil, 0o644); err != nil {
		return err
	}
	if _, err := git(ctx, dir, "add", "-f", ".sfosbuild/.gitkeep"); err != nil {
		return err
	}
	return commit(ctx, dir, ".sfosbuild/.gitkeep")
}

func applyPatches(ctx context.Context, dir, patchesDir string) error {
	patches, err := patchFiles(patchesDir)
	if err != nil {
		return err
	}
	for _, p := range patches {
		log.Printf("patch %s", p.rel)
		if _, err := git(ctx, dir, "apply", p.abs); err != nil {
			return fmt.Errorf("patch failed: %s: %w", p.rel, err)
		}
		if err := commitDirtySubmodules(ctx, dir); err != nil {
			return fmt.Errorf("commit submodule %s: %w", p.rel, err)
		}
		if _, err := git(ctx, dir, "add", "-A"); err != nil {
			return err
		}
		if err := commit(ctx, dir, p.rel); err != nil {
			return fmt.Errorf("commit %s: %w", p.rel, err)
		}
	}
	return nil
}

type patch struct {
	abs string
	rel string
}

func patchFiles(dir string) ([]patch, error) {
	st, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("patches is not a directory")
	}
	var out []patch
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".patch") {
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(dir), path)
		if err != nil {
			return err
		}
		out = append(out, patch{abs: path, rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func applyOverlay(ctx context.Context, dir, overlayDir string) error {
	st, err := os.Stat(overlayDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("overlay: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("overlay is not a directory")
	}
	log.Printf("overlay")
	var rels []string
	err = filepath.WalkDir(overlayDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(overlayDir, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := copyFile(path, dest); err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	if len(rels) == 0 {
		return fmt.Errorf("overlay is empty")
	}
	sort.Strings(rels)
	args := append([]string{"add", "-f", "--"}, rels...)
	if _, err := git(ctx, dir, args...); err != nil {
		return err
	}
	return commit(ctx, dir, "overlay")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func commitDirtySubmodules(ctx context.Context, dir string) error {
	script := `if ! git diff --quiet; then git add -A && git -c user.name=ftuna -c user.email=ftuna@localhost -c commit.gpgsign=false commit -m patch; fi`
	_, err := git(ctx, dir, "submodule", "foreach", "--recursive", script)
	return err
}

// CommitPath records one file written after the overlay, such as the repo hook.
func CommitPath(ctx context.Context, dir, rel, message string) error {
	if _, err := git(ctx, dir, "add", "-f", "--", rel); err != nil {
		return err
	}
	return commit(ctx, dir, message)
}

func commit(ctx context.Context, dir, message string) error {
	_, err := git(ctx, dir,
		"-c", "user.name=ftuna",
		"-c", "user.email=ftuna@localhost",
		"-c", "commit.gpgsign=false",
		"commit", "-m", message,
	)
	return err
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return out, err
		}
		return out, fmt.Errorf("%w: %s", err, msg)
	}
	return out, nil
}
