package pipeline

import "testing"

func TestScratchPathOK(t *testing.T) {
	ok := []string{
		"aarch64/app.rpm",
		"aarch64/github.com/owner/name/app.rpm",
		"armv7hl/repodata/repomd.xml",
		"i486/repodata/ab-primary.xml.gz",
		"info.json.xz",
	}
	bad := []string{
		"meta.json",
		"aarch64/repodata/pkg.sqlite.bz2",
		"aarch64/repomd.xml.asc",
		"../x.rpm",
		"notes.txt",
	}
	for _, name := range ok {
		if !scratchPathOK(name) {
			t.Errorf("want allow %s", name)
		}
	}
	for _, name := range bad {
		if scratchPathOK(name) {
			t.Errorf("want reject %s", name)
		}
	}
}
