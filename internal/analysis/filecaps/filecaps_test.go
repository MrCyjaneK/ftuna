package filecaps

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestCaps(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{Path: "/usr/bin/bind", Kind: check.KindFile, Caps: "cap_net_bind_service=ep"},
		{Path: "/usr/bin/admin", Kind: check.KindFile, Caps: "cap_sys_admin,cap_net_bind_service=ep"},
		{Path: "/usr/bin/plain", Kind: check.KindFile},
		{Path: "/usr/bin/note", Kind: check.KindFile, Caps: "cap_net_broadcast=p"},
	})
	if rep.Score != scoreRoot || len(rep.Issues) != 3 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	if rep.Issues[0].Detail != "cap_net_bind_service=ep" || rep.Issues[1].Code != "filecaps" {
		t.Fatalf("%+v", rep.Issues)
	}
	if got := m.Analyze([]check.File{{Path: "/usr/bin/plain", Kind: check.KindFile, Caps: ""}}); got.Score != check.Neutral {
		t.Fatalf("none %+v", got)
	}
	owned := m.Analyze([]check.File{{
		Path: "/usr/bin/bind", Kind: check.KindFile, Mode: 0100755, User: "root", Group: "root",
		Caps: "cap_net_bind_service=ep",
	}})
	if owned.Score != scoreMedium || owned.Issues[0].Detail != "cap_net_bind_service=ep mode 0100755 user root group root" {
		t.Fatalf("owned %+v", owned)
	}
	unknown := m.Analyze([]check.File{{Path: "/usr/bin/odd", Kind: check.KindFile, Caps: "cap_net_broadcast=p"}})
	if unknown.Score != scoreRoot {
		t.Fatalf("unknown %+v", unknown)
	}
}
