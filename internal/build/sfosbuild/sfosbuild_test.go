package sfosbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandArgsStampBeforeVersion(t *testing.T) {
	args := commandArgs("/tmp/rpms", "5.1.0.11", "aarch64,armv7hl,i486", "rpm/app.yaml")
	got := strings.Join(args, " ")
	want := "-o /tmp/rpms --stamp 5.1.0.11 aarch64,armv7hl,i486 rpm/app.yaml"
	if got != want {
		t.Fatalf("args %q", got)
	}
}

func TestPackageFilesSpecBuildsOnlyThatFile(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "rpm"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"flutter-sfos-3.44.0.spec", "flutter-sfos-3.47.7.spec"} {
		if err := os.WriteFile(filepath.Join(project, "rpm", name), []byte("Name: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := packageFiles(project, "rpm/flutter-sfos-3.47.7.spec")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "rpm/flutter-sfos-3.47.7.spec" {
		t.Fatalf("files %q", got)
	}
}

func TestPackageFilesDirectoryGlobsRPM(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "rpm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "rpm", "app.yaml"), []byte("Name: app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "rpm", "extra.spec"), []byte("Name: extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := packageFiles(project, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "rpm/app.yaml" {
		t.Fatalf("files %q", got)
	}
}

func TestPackageFilesMissingSpec(t *testing.T) {
	project := t.TempDir()
	if _, err := packageFiles(project, "rpm/missing.spec"); err == nil {
		t.Fatal("expected missing spec to fail")
	}
}

func TestTailWriterKeepsEnd(t *testing.T) {
	w := &tailWriter{limit: 4}
	if _, err := w.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := w.String(); got != "cdef" {
		t.Fatalf("tail %q", got)
	}
	if _, err := w.Write([]byte("gh")); err != nil {
		t.Fatal(err)
	}
	if got := w.String(); got != "efgh" {
		t.Fatalf("tail %q", got)
	}
}
