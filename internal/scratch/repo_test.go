package scratch

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/rpmpack"
)

func TestStageFlatRPM(t *testing.T) {
	dir := t.TempDir()
	writeScratchRPM(t, filepath.Join(dir, "app-1.0.0-1.aarch64.rpm"), "aarch64")
	writeScratchRPM(t, filepath.Join(dir, "doc-1.0.0-1.noarch.rpm"), "noarch")
	staged, cleanup, err := stageRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if staged == dir {
		t.Fatal("flat directory was uploaded unchanged")
	}
	if _, err := os.Stat(filepath.Join(dir, "aarch64", "repodata", "repomd.xml")); !os.IsNotExist(err) {
		t.Fatalf("source was modified: %v", err)
	}
	for _, rel := range []string{
		"aarch64/app-1.0.0-1.aarch64.rpm",
		"aarch64/doc-1.0.0-1.noarch.rpm",
		"aarch64/repodata/repomd.xml",
	} {
		if _, err := os.Stat(filepath.Join(staged, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(staged, "armv7hl")); !os.IsNotExist(err) {
		t.Fatal("noarch was copied to an architecture that has no packages")
	}
}

func TestStageNoarchOnly(t *testing.T) {
	dir := t.TempDir()
	writeScratchRPM(t, filepath.Join(dir, "doc-1.0.0-1.noarch.rpm"), "noarch")
	staged, cleanup, err := stageRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, arch := range scratchArches {
		rel := arch + "/repodata/repomd.xml"
		if _, err := os.Stat(filepath.Join(staged, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
}

func TestStageArchWithoutRepodata(t *testing.T) {
	dir := t.TempDir()
	writeScratchRPM(t, filepath.Join(dir, "aarch64", "app-1.0.0-1.aarch64.rpm"), "aarch64")
	staged, cleanup, err := stageRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(staged, "aarch64", "repodata", "repomd.xml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "aarch64", "repodata", "repomd.xml")); !os.IsNotExist(err) {
		t.Fatalf("source was modified: %v", err)
	}
}

func TestStageReadyTree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "aarch64", "app.rpm"), []byte{0xed, 0xab, 0xee, 0xdb})
	writeFile(t, filepath.Join(dir, "aarch64", "repodata", "repomd.xml"), []byte("<repomd/>"))
	staged, cleanup, err := stageRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if staged != dir {
		t.Fatalf("staged %s", staged)
	}
	if _, err := os.Stat(filepath.Join(dir, "aarch64", "app.rpm")); err != nil {
		t.Fatal(err)
	}
}

func writeScratchRPM(t *testing.T, path, arch string) {
	t.Helper()
	rpm, err := rpmpack.NewRPM(rpmpack.RPMMetaData{
		Name: "app", Version: "1.0.0", Release: "1", Arch: arch, Summary: "An app",
	})
	if err != nil {
		t.Fatal(err)
	}
	rpm.AddFile(rpmpack.RPMFile{Name: "/usr/bin/app", Body: []byte("app"), Mode: 0755, Owner: "root", Group: "root"})
	var buf bytes.Buffer
	if err := rpm.Write(&buf); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.Bytes())
}
