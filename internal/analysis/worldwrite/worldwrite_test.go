package worldwrite

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestModes(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{Path: "/usr/bin/demo", Kind: check.KindFile, Mode: 0100755, User: "root", Group: "root"},
		{Path: "/usr/bin/link", Kind: check.KindSymlink, Mode: 0120777},
		{Path: "/tmp", Kind: check.KindDir, Mode: 0041777, User: "root", Group: "root"},
		{Path: "/var/lib/demo", Kind: check.KindDir, Mode: 0040777, User: "root", Group: "root"},
		{Path: "/usr/bin/hook", Kind: check.KindFile, Mode: 0100777, User: "root", Group: "root"},
		{Path: "/var/lib/demo/state", Kind: check.KindFile, Mode: 0100666, User: "nemo", Group: "nemo"},
		{Path: "%post", Kind: check.KindScriptlet, Mode: 0100666},
	})
	if rep.Score != 0 || len(rep.Issues) != 4 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	got := map[string]string{}
	for _, issue := range rep.Issues {
		got[issue.Path] = issue.Detail
	}
	if got["/tmp"] != "/tmp mode 041777 user root group root dir sticky" {
		t.Fatalf("tmp %s", got["/tmp"])
	}
	if got["/var/lib/demo"] != "/var/lib/demo mode 040777 user root group root dir" {
		t.Fatalf("dir %s", got["/var/lib/demo"])
	}
	if got["/usr/bin/hook"] != "/usr/bin/hook mode 0100777 user root group root exec" {
		t.Fatalf("exec %s", got["/usr/bin/hook"])
	}
	if got["/var/lib/demo/state"] != "/var/lib/demo/state mode 0100666 user nemo group nemo file" {
		t.Fatalf("file %s", got["/var/lib/demo/state"])
	}
	plain := m.Analyze([]check.File{{Path: "/usr/bin/demo", Kind: check.KindFile, Mode: 0100755}})
	if plain.Score != check.Neutral || len(plain.Issues) != 0 {
		t.Fatalf("plain %+v", plain)
	}
	config := m.Analyze([]check.File{{Path: "/etc/demo.conf", Kind: check.KindFile, Mode: 0100666, User: "root", Group: "root"}})
	if config.Score != 0 || config.Issues[0].Detail != "/etc/demo.conf mode 0100666 user root group root file" {
		t.Fatalf("config %+v", config)
	}
}
