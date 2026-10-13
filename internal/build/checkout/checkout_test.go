package checkout

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSfosbuildGitkeepSkipsExistingDir(t *testing.T) {
	dir := gitRepo(t)
	write(t, filepath.Join(dir, ".sfosbuild", "image", "10-prepare.sh"), "#!/bin/sh\n")
	gitCommit(t, dir, "init")

	if err := ensureSfosbuildGitkeep(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	tracked := gitOut(t, dir, "ls-files", ".sfosbuild/.gitkeep")
	if strings.TrimSpace(tracked) != "" {
		t.Fatalf("unexpected .gitkeep: %q", tracked)
	}
}

func TestPatchInsideSubmoduleIsCommitted(t *testing.T) {
	sub := gitRepo(t)
	write(t, filepath.Join(sub, "CMakeLists.txt"), "cargo rustc --profile release\n")
	gitCommit(t, sub, "init")
	subCommit := strings.TrimSpace(gitOut(t, sub, "rev-parse", "HEAD"))

	origin := gitRepo(t)
	run(t, origin, "git", "-c", "protocol.file.allow=always", "submodule", "add", sub, "lottieqt")
	gitCommit(t, origin, "add submodule")

	patches := filepath.Join(t.TempDir(), "patches")
	write(t, filepath.Join(patches, "lottieqt.patch"), `
diff --git a/lottieqt/CMakeLists.txt b/lottieqt/CMakeLists.txt
--- a/lottieqt/CMakeLists.txt
+++ b/lottieqt/CMakeLists.txt
@@ -1 +1 @@
-cargo rustc --profile release
+cargo rustc --offline --profile release
`)
	if err := applyPatches(context.Background(), origin, patches); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(origin, "lottieqt", "CMakeLists.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "cargo rustc --offline --profile release\n" {
		t.Fatalf("CMakeLists %q", body)
	}
	link := strings.TrimSpace(gitOut(t, origin, "rev-parse", "HEAD:lottieqt"))
	if link == subCommit {
		t.Fatal("parent still points at the unpatched submodule commit")
	}
	got := gitOut(t, filepath.Join(origin, "lottieqt"), "show", link+":CMakeLists.txt")
	if got != "cargo rustc --offline --profile release\n" {
		t.Fatalf("archived submodule file %q", got)
	}
}

func TestPatchFailureNamesThePatch(t *testing.T) {
	dir := gitRepo(t)
	write(t, filepath.Join(dir, "README"), "hello\n")
	gitCommit(t, dir, "init")

	patches := filepath.Join(t.TempDir(), "patches")
	write(t, filepath.Join(patches, "missing.txt.patch"), `
diff --git a/missing.txt b/missing.txt
--- a/missing.txt
+++ b/missing.txt
@@ -1 +1 @@
-old
+new
`)
	err := applyPatches(context.Background(), dir, patches)
	if err == nil || !strings.Contains(err.Error(), "patches/missing.txt.patch") {
		t.Fatalf("error %v", err)
	}
	log := gitLog(t, dir)
	if strings.Contains(log, "missing.txt.patch") {
		t.Fatalf("failed patch was committed:\n%s", log)
	}
}

func TestPrepareCommitsPatchesAndOverlay(t *testing.T) {
	origin := gitRepo(t)
	write(t, filepath.Join(origin, ".gitignore"), ".sfosbuild/\n")
	write(t, filepath.Join(origin, "README"), "hello\n")
	gitCommit(t, origin, "init")

	changed := filepath.Join(t.TempDir(), "changed")
	run(t, "", "git", "clone", origin, changed)
	write(t, filepath.Join(changed, "README"), "hello\nworld\n")
	diff, err := exec.Command("git", "-C", changed, "diff").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) == 0 {
		t.Fatal("empty diff")
	}

	recipe := t.TempDir()
	write(t, filepath.Join(recipe, "patches", "README.patch"), string(diff))
	write(t, filepath.Join(recipe, "overlay", "extra.txt"), "overlay\n")

	commit := strings.TrimSpace(gitOut(t, origin, "rev-parse", "HEAD"))
	dest := filepath.Join(t.TempDir(), "src")
	if err := Prepare(context.Background(), origin, commit, filepath.Join(recipe, "patches"), filepath.Join(recipe, "overlay"), dest); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(context.Background(), origin, commit, filepath.Join(recipe, "patches"), filepath.Join(recipe, "overlay"), dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "README"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello\nworld\n" {
		t.Fatalf("README %q", body)
	}
	tracked := gitOut(t, dest, "ls-files", ".sfosbuild/.gitkeep")
	if strings.TrimSpace(tracked) != ".sfosbuild/.gitkeep" {
		t.Fatalf(".sfosbuild/.gitkeep not tracked: %q", tracked)
	}
	log := gitLog(t, dest)
	for _, want := range []string{"overlay", "patches/README.patch", ".sfosbuild/.gitkeep"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func TestCacheDir(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	got, err := CacheDir("github.com/sailfishos/gallery")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cache, "ftuna", "src", "github.com_sailfishos_gallery")
	if got != want {
		t.Fatalf("cache %s", got)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, "", "git", "-c", "init.defaultBranch=main", "init", dir)
	return dir
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git",
		"-c", "user.name=ftuna",
		"-c", "user.email=ftuna@localhost",
		"-c", "commit.gpgsign=false",
		"commit", "-m", msg,
	)
}

func gitLog(t *testing.T, dir string) string {
	t.Helper()
	return gitOut(t, dir, "log", "--format=%s")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
