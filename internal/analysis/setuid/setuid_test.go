package setuid

import (
	"strings"
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestBits(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{{
		Path: "/usr/bin/both", Kind: check.KindFile, Mode: 0106755, User: "root", Group: "root",
	}})
	got := rep.Issues
	if rep.Score != 0 || len(got) != 2 || got[0].Code != "setuid" || got[1].Code != "setgid" {
		t.Fatalf("%d %+v", rep.Score, got)
	}
	if got[0].Detail != "/usr/bin/both mode 0106755 user root group root file" {
		t.Fatalf("detail %s", got[0].Detail)
	}
	dir := m.Analyze([]check.File{{Path: "/var/empty", Kind: check.KindDir, Mode: 042755, User: "root", Group: "root"}})
	if dir.Score != scoreSetgidDir || len(dir.Issues) != 1 || dir.Issues[0].Code != "setgid" {
		t.Fatalf("dir %+v", dir)
	}
	user := m.Analyze([]check.File{{Path: "/usr/bin/helper", Kind: check.KindFile, Mode: 02755, User: "nemo", Group: "nemo"}})
	if user.Score != scoreSetgidUser || len(user.Issues) != 1 {
		t.Fatalf("user %+v", user)
	}
	both := m.Analyze([]check.File{
		{Path: "/usr/bin/helper", Kind: check.KindFile, Mode: 02755, User: "nemo", Group: "nemo"},
		{Path: "/usr/bin/demo", Kind: check.KindFile, Mode: 04755, User: "root", Group: "root"},
	})
	if both.Score != 0 || len(both.Issues) != 2 {
		t.Fatalf("worst %+v", both)
	}
	plain := m.Analyze([]check.File{{Path: "/usr/bin/plain", Kind: check.KindFile, Mode: 0100755}})
	if plain.Score != check.Neutral || len(plain.Issues) != 0 {
		t.Fatalf("plain %+v", plain)
	}
	scriptlet := m.Analyze([]check.File{{Path: "%post", Kind: check.KindScriptlet, Mode: 04000}})
	if scriptlet.Score != check.Neutral || len(scriptlet.Issues) != 0 {
		t.Fatalf("scriptlet %+v", scriptlet)
	}
	link := m.Analyze([]check.File{{Path: "/usr/bin/link", Kind: check.KindSymlink, Mode: 0124755, User: "root", Group: "root"}})
	if link.Score != check.Neutral || len(link.Issues) != 0 {
		t.Fatalf("link %+v", link)
	}
	script := m.Analyze([]check.File{{
		Path: "/usr/bin/demo", Kind: check.KindFile, Mode: 04755, User: "nemo", Group: "nemo",
		Body: []byte("#!/bin/sh\necho hi\n"),
	}})
	if script.Score != scoreSetuidRoot || len(script.Issues) != 1 || !strings.HasSuffix(script.Issues[0].Detail, " script") {
		t.Fatalf("script %+v", script)
	}
}
