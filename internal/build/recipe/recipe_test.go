package recipe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverPinsCommitAndArches(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "sailfishos", "sailfish-components-gallery")
	if err := os.MkdirAll(filepath.Join(dir, "overlay"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: Sailfish Components Gallery\ncommit: aca93b6cfecb02e9d13955e71b51460e7d303424\nai: H\ndescription: Gallery components\npath: rpm/gallery.yaml\n# later fields are ignored\nnote: keep\n"
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("recipes: %d", len(got))
	}
	r := got[0]
	if r.ID != "github.com/sailfishos/sailfish-components-gallery" {
		t.Fatalf("id %s", r.ID)
	}
	if r.Name != "Sailfish Components Gallery" {
		t.Fatalf("name %s", r.Name)
	}
	if r.URL != "https://github.com/sailfishos/sailfish-components-gallery" {
		t.Fatalf("url %s", r.URL)
	}
	if r.Commit != "aca93b6cfecb02e9d13955e71b51460e7d303424" {
		t.Fatalf("commit %s", r.Commit)
	}
	if ArchArg(r.Arches) != "all" {
		t.Fatalf("arches %v", r.Arches)
	}
	if r.Icon != "" || r.Install != nil {
		t.Fatalf("unexpected art %#v", r)
	}
	if r.AI != "H" {
		t.Fatalf("ai %s", r.AI)
	}
	if r.Description != "Gallery components" {
		t.Fatalf("description %q", r.Description)
	}
	if r.Path != "rpm/gallery.yaml" {
		t.Fatalf("path %q", r.Path)
	}
}

func TestDiscoverIconScreenshotsAndInstall(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "piggz", "harbour-advanced-camera")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `name: Advanced Camera
commit: cdcef3fb69267178f3f224b12f5f6b862a205f6e
ai: A
icon: https://raw.githubusercontent.com/piggz/harbour-advanced-camera/cdcef3fb69267178f3f224b12f5f6b862a205f6e/harbour-advanced-camera.svg
screenshots:
  - https://raw.githubusercontent.com/piggz/harbour-advanced-camera/cdcef3fb69267178f3f224b12f5f6b862a205f6e/screenshots/screenshot1.png
description: |
  Advanced camera.

  - Focus
install:
  - harbour-advanced-camera
path: rpm/harbour-advanced-camera.yaml
`
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	r := got[0]
	if r.Icon == "" || len(r.Screenshots) != 1 || len(r.Install) != 1 || r.Install[0] != "harbour-advanced-camera" || r.AI != "A" {
		t.Fatalf("%#v", r)
	}
	if r.Description != "Advanced camera.\n\n- Focus" {
		t.Fatalf("description %q", r.Description)
	}
}

func TestDiscoverPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "mrcyjanek", "sfos_orangeflex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: Orange Flex\ncommit: 1023ab50a085e6307f818971276818dd176eecb2\nai: H\ndescription: Orange Flex\npath: ./apps/sfos/rpm/orange.yaml\n"
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != "apps/sfos/rpm/orange.yaml" {
		t.Fatalf("path %q", got[0].Path)
	}
}

func TestRejectsPathOutsideRepository(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "build.yaml")
	base := "name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: An app\n"
	for _, yaml := range []string{
		base,
		base + "path: .\n",
		base + "path: apps/sfos\n",
		base + "path: rpm/app.spec\n",
		base + "path: /tmp/apps.yaml\n",
		base + "path: ../app.yaml\n",
		base + "path: apps/../../etc/app.yaml\n",
	} {
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(root); err == nil {
			t.Fatalf("expected path to fail for %q", yaml)
		}
	}
}

func TestRejectsForeignIconURL(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: An app\nicon: https://example.com/icon.svg\n"
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("expected error")
	}
}

func TestRejectsMissingOrUnknownAI(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "build.yaml")
	if err := os.WriteFile(path, []byte("name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\ndescription: An app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("expected missing ai to fail")
	}
	if err := os.WriteFile(path, []byte("name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\ndescription: An app\nai: Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("expected unknown ai to fail")
	}
}

func TestRejectsMissingName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "build.yaml")
	for _, yaml := range []string{
		"commit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: An app\n",
		"name: \"  \\n\"\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: An app\n",
	} {
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(root); err == nil {
			t.Fatalf("expected missing name to fail for %q", yaml)
		}
	}
}

func TestRejectsMissingDescription(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "build.yaml")
	for _, yaml := range []string{
		"name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\n",
		"name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: \"  \\n\"\n",
	} {
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(root); err == nil {
			t.Fatalf("expected missing description to fail for %q", yaml)
		}
	}
}

func TestRejectsShortCommit(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte("name: An app\ncommit: abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverSeveralRecipesInOneRepository(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "mono")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := "commit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: A package\n"
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte("name: The app\n"+base+"path: rpm/app.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := "name: The library\n" + base + "path: libs/foo/rpm/lib.yaml\nrepos:\n  - https://repo-oss.ftuna.eu/{version}/{arch}/\n  - https://repo.sailfishos.org/obs/sailfishos:/chum/{version}_{arch}/\n"
	if err := os.WriteFile(filepath.Join(dir, "libfoo.yaml"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("recipes %d", len(got))
	}
	if got[0].ID != "github.com/example/mono" || got[0].URL != "https://github.com/example/mono" || got[0].Repos != nil {
		t.Fatalf("app %#v", got[0])
	}
	libRecipe := got[1]
	if libRecipe.ID != "github.com/example/mono/libfoo" || libRecipe.URL != "https://github.com/example/mono" {
		t.Fatalf("id %s url %s", libRecipe.ID, libRecipe.URL)
	}
	if libRecipe.Path != "libs/foo/rpm/lib.yaml" || len(libRecipe.Repos) != 2 {
		t.Fatalf("%#v", libRecipe)
	}
	if filepath.Base(libRecipe.File) != "libfoo.yaml" {
		t.Fatalf("file %s", libRecipe.File)
	}
}

func TestDiscoverRejectsDuplicateRecipeID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app", "foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name: A package\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: A package\npath: rpm/foo.yaml\n"
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "foo.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("expected duplicate id")
	}
}

func TestRejectsRepoURL(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "build.yaml")
	base := "name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: An app\npath: rpm/app.yaml\nrepos:\n"
	for _, repo := range []string{
		"  - http://repo-oss.ftuna.eu/{version}/{arch}/\n",
		"  - https://repo-oss.ftuna.eu/{version}/{arch}/;id\n",
		"  - https://repo-oss.ftuna.eu/{version}/{arch}/;\n",
		"  - https://repo-oss.ftuna.eu/{version}/{arch}/;https://repo-oss.ftuna.eu/a.key;https://example.com/b.key\n",
		"  - https://repo-oss.ftuna.eu/{nope}/{arch}/\n",
		"  - https://{version}.example/{arch}/\n",
		"  - https://repo-oss.ftuna.eu/{version}/{arch}/\n  - https://repo-oss.ftuna.eu/{version}/{arch}/\n",
	} {
		if err := os.WriteFile(path, []byte(base+repo), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(root); err == nil {
			t.Fatalf("expected repo to fail for %q", repo)
		}
	}
}

func TestRootFromEnv(t *testing.T) {
	t.Setenv("FTUNA_RECIPES", "")
	if _, err := Root(); err == nil {
		t.Fatal("unset")
	}
	dir := t.TempDir()
	t.Setenv("FTUNA_RECIPES", dir)
	got, err := Root()
	if err != nil || got != dir {
		t.Fatalf("%v %q", err, got)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FTUNA_RECIPES", file)
	if _, err := Root(); err == nil {
		t.Fatal("file")
	}
	t.Setenv("FTUNA_RECIPES", filepath.Join(dir, "missing"))
	if _, err := Root(); err == nil {
		t.Fatal("missing")
	}
}

func TestRepoScriptExpand(t *testing.T) {
	dir := t.TempDir()
	repos := []string{
		"https://repo-oss.ftuna.eu/{version}/{arch}/",
		"https://repo.sailfishos.org/obs/sailfishos:/chum/{version}_{arch}/",
		"https://example.com/{release}/{arch}/",
	}
	if err := InstallRepos(dir, repos); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, ".sfosbuild", "image", "00-ftuna-repos.sh")
	now := expandRepos(t, script, "5.1.0.11", "aarch64")
	for _, want := range []string{
		"https://repo-oss.ftuna.eu/5.1.0.11/aarch64/",
		"https://repo.sailfishos.org/obs/sailfishos:/chum/5.1_aarch64/",
		"https://example.com/5.1/aarch64/",
	} {
		if !strings.Contains(now, want) {
			t.Fatalf("missing %s in %s", want, now)
		}
	}
	old := expandRepos(t, script, "4.5.0.24", "armv7hl")
	if !strings.Contains(old, "https://repo.sailfishos.org/obs/sailfishos:/chum/4.5.0.24_armv7hl/") {
		t.Fatalf("old chum %s", old)
	}
	if !strings.Contains(old, "https://example.com/4.5/armv7hl/") {
		t.Fatalf("old release %s", old)
	}
	if err := InstallRepos(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAcceptsRepoKey(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "example", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name: An app\ncommit: 0123456789abcdef0123456789abcdef01234567\nai: H\ndescription: An app\npath: rpm/app.yaml\nrepos:\n  - https://repo-oss.ftuna.eu/{version}/{arch}/;https://repo-oss.ftuna.eu/ftuna-oss.key\n"
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://repo-oss.ftuna.eu/{version}/{arch}/;https://repo-oss.ftuna.eu/ftuna-oss.key"
	if len(got) != 1 || len(got[0].Repos) != 1 || got[0].Repos[0] != want {
		t.Fatalf("%#v", got)
	}
}

func TestRepoKeyAddedWithGPGKey(t *testing.T) {
	dir := t.TempDir()
	repos := []string{
		"https://repo-oss.ftuna.eu/{version}/{arch}/;https://repo-oss.ftuna.eu/ftuna-oss.key",
		"https://example.com/{release}/{arch}/",
	}
	if err := InstallRepos(dir, repos); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, ".sfosbuild", "image", "00-ftuna-repos.sh")
	out := expandRepos(t, script, "5.1.0.11", "aarch64")
	repo, err := os.ReadFile("/tmp/ftuna-extra-1.repo")
	if err != nil {
		t.Fatal(err)
	}
	text := string(repo)
	for _, want := range []string{
		"baseurl=https://repo-oss.ftuna.eu/5.1.0.11/aarch64/\n",
		"gpgkey=https://repo-oss.ftuna.eu/ftuna-oss.key\n",
		"gpgcheck=1\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	addLine := "zypper --non-interactive ar /tmp/ftuna-extra-1.repo\n"
	refreshLine := "zypper --non-interactive --gpg-auto-import-keys refresh ftuna-extra-1\n"
	addAt := strings.Index(out, addLine)
	refreshAt := strings.Index(out, refreshLine)
	if addAt < 0 || refreshAt < 0 || addAt > refreshAt {
		t.Fatalf("add before refresh:\n%s", out)
	}
	if !strings.Contains(out, "zypper --non-interactive ar -f https://example.com/5.1/aarch64/ ftuna-extra-2\n") {
		t.Fatalf("second repo:\n%s", out)
	}
}

func expandRepos(t *testing.T, script, version, arch string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `zypper() { printf 'zypper %s\n' "$*"; }; rpm() { printf 'rpm %s\n' "$*"; }; . "$1"`, "sh", script)
	cmd.Env = append(os.Environ(), "SFOS_VERSION="+version, "SFOS_ARCH="+arch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return string(out)
}

func TestValidVersion(t *testing.T) {
	if !ValidVersion("5.1.0.11") {
		t.Fatal("5.1.0.11")
	}
	for _, bad := range []string{"", "5.1.0", "5.1.0.11/evil", "../5", "v5.1.0.11"} {
		if ValidVersion(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}
