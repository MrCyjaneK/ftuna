package hooks

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestPaths(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{Path: "/etc/ld.so.preload", Kind: check.KindFile, Mode: 0100644, User: "root", Group: "root"},
		{Path: "/etc/pam.d/demo", Kind: check.KindFile, Mode: 0100644, User: "root", Group: "root"},
		{Path: "/etc/profile.d/demo.sh", Kind: check.KindFile, Mode: 0100644, User: "root", Group: "root"},
		{Path: "/usr/lib/systemd/system-generators/demo", Kind: check.KindFile, Mode: 0100755, User: "root", Group: "root"},
		{Path: "/usr/lib/systemd/user-generators/demo", Kind: check.KindFile, Mode: 0100755, User: "nemo", Group: "nemo"},
		{Path: "/usr/lib/modules/demo.ko", Kind: check.KindFile, Mode: 0100644, User: "root", Group: "root"},
		{Path: "/dev/demo", Kind: check.KindFile, Mode: 0020660, User: "root", Group: "root"},
		{Path: "/usr/bin/demo", Kind: check.KindFile, Mode: 0100755, User: "root", Group: "root"},
		{Path: "/etc/pam.d", Kind: check.KindDir, Mode: 040755, User: "root", Group: "root"},
	})
	if rep.Score != scoreLinker || len(rep.Issues) != 7 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	byPath := map[string]check.Issue{}
	for _, issue := range rep.Issues {
		byPath[issue.Path] = issue
	}
	if byPath["/etc/ld.so.preload"].Code != "hooks.linker" || byPath["/etc/pam.d/demo"].Code != "hooks.pam" {
		t.Fatalf("%v", byPath)
	}
	if byPath["/etc/profile.d/demo.sh"].Code != "hooks.profile" || byPath["/usr/lib/modules/demo.ko"].Code != "hooks.module" {
		t.Fatalf("%v", byPath)
	}
	if byPath["/usr/lib/systemd/system-generators/demo"].Detail != "scope=system mode 0100755 user root group root" {
		t.Fatalf("generator %+v", byPath["/usr/lib/systemd/system-generators/demo"])
	}
	if byPath["/usr/lib/systemd/user-generators/demo"].Detail != "scope=user mode 0100755 user nemo group nemo" {
		t.Fatalf("user generator %+v", byPath["/usr/lib/systemd/user-generators/demo"])
	}
	if byPath["/dev/demo"].Code != "hooks.device" {
		t.Fatalf("device %+v", byPath["/dev/demo"])
	}
	user := m.Analyze([]check.File{{
		Path: "/usr/lib/systemd/user-generators/demo", Kind: check.KindFile, Mode: 0100755, User: "nemo", Group: "nemo",
	}})
	if user.Score != scoreUserGen {
		t.Fatalf("user %d", user.Score)
	}
	if got := m.Analyze(nil); got.Score != check.Neutral {
		t.Fatalf("none %+v", got)
	}
}
