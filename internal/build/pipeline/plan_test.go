package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
)

func TestPhoneReadableMetadataRejectsZstd(t *testing.T) {
	if PhoneReadableMetadata([]byte(`<location href="repodata/abc-primary.xml.zst"/>`)) {
		t.Fatal("zstd metadata must be rebuilt")
	}
	if !PhoneReadableMetadata([]byte(`<location href="repodata/abc-primary.xml.gz"/>`)) {
		t.Fatal("gzip metadata is readable")
	}
}

func TestNeedsBuildSkipsPublishedCommit(t *testing.T) {
	r := recipe.Recipe{
		ID:     "github.com/example/app",
		Name:   "Example",
		Commit: "0123456789abcdef0123456789abcdef01234567",
		Arches: []string{"aarch64"},
	}
	man := Manifest{Recipes: map[string]RecipeBuild{
		r.ID: {
			Commit: r.Commit,
			Arches: map[string]ArchBuild{
				"aarch64": {RPMs: []RPMRec{{File: "github.com/example/app/app-1.aarch64.rpm", Name: "app"}}},
			},
		},
	}}
	exists := func(key string) (bool, error) {
		want := ArchPrefix("5.1.0.11", "aarch64") + "github.com/example/app/app-1.aarch64.rpm"
		if key != want {
			return false, fmt.Errorf("unexpected key %s", key)
		}
		return true, nil
	}
	need, err := NeedsBuild(r, man, "5.1.0.11", false, exists)
	if err != nil {
		t.Fatal(err)
	}
	if need {
		t.Fatal("expected skip")
	}
	need, err = NeedsBuild(r, man, "5.1.0.11", true, exists)
	if err != nil {
		t.Fatal(err)
	}
	if !need {
		t.Fatal("force should rebuild")
	}
	missing := func(string) (bool, error) { return false, nil }
	need, err = NeedsBuild(r, man, "5.1.0.11", false, missing)
	if err != nil {
		t.Fatal(err)
	}
	if !need {
		t.Fatal("missing object should rebuild")
	}
	flat := man
	flat.Recipes[r.ID].Arches["aarch64"].RPMs[0].File = "app-1.aarch64.rpm"
	need, err = NeedsBuild(r, flat, "5.1.0.11", false, exists)
	if err != nil {
		t.Fatal(err)
	}
	if !need {
		t.Fatal("flat rpm path should rebuild")
	}
}

func TestPlaceBuiltNestsByRecipe(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "app-1.aarch64.rpm"), []byte("rpm"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := placeBuilt(out, []deps.RPM{{File: "app-1.aarch64.rpm", Name: "app"}}, "github.com/example/app", []string{"aarch64"}, repo)
	if err != nil {
		t.Fatal(err)
	}
	want := "github.com/example/app/app-1.aarch64.rpm"
	if got["aarch64"][0].File != want {
		t.Fatalf("file %s", got["aarch64"][0].File)
	}
	if _, err := os.Stat(filepath.Join(repo, "aarch64", "github.com", "example", "app", "app-1.aarch64.rpm")); err != nil {
		t.Fatal(err)
	}
}

func TestArchOf(t *testing.T) {
	arch, err := ArchOf("sailfish-components-gallery-qt5-1.3.0-1.aarch64.rpm")
	if err != nil || arch != "aarch64" {
		t.Fatalf("%s %v", arch, err)
	}
	if _, err := ArchOf("not-an-rpm.txt"); err == nil {
		t.Fatal("expected error")
	}
}
