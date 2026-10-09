package recipe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverPinsCommitAndArches(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "sailfishos", "sailfish-components-gallery")
	if err := os.MkdirAll(filepath.Join(dir, "overlay"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: Sailfish Components Gallery\ncommit: aca93b6cfecb02e9d13955e71b51460e7d303424\nai: H\ndescription: Gallery components\n# later fields are ignored\nnote: keep\n"
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
	if r.Path != "." {
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
	yaml := "name: Orange Flex\ncommit: 1023ab50a085e6307f818971276818dd176eecb2\nai: H\ndescription: Orange Flex\npath: ./apps/sfos\n"
	if err := os.WriteFile(filepath.Join(dir, "build.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != "apps/sfos" {
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
		base + "path: /tmp/apps\n",
		base + "path: ..\n",
		base + "path: ../apps\n",
		base + "path: apps/../../etc\n",
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
