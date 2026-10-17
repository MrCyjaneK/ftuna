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
	if len(rep.Issues) != 3 || rep.Issues[0].Color != check.ColorRed || rep.Issues[1].Color != check.ColorRed || rep.Issues[2].Color != check.ColorRed {
		t.Fatalf("%+v", rep.Issues)
	}
	if rep.Issues[0].Detail != "cap_net_bind_service=ep" || rep.Issues[1].Code != "filecaps" {
		t.Fatalf("%+v", rep.Issues)
	}
	if got := m.Analyze([]check.File{{Path: "/usr/bin/plain", Kind: check.KindFile, Caps: ""}}); len(got.Issues) != 0 {
		t.Fatalf("none %+v", got)
	}
	owned := m.Analyze([]check.File{{
		Path: "/usr/bin/bind", Kind: check.KindFile, Mode: 0100755, User: "root", Group: "root",
		Caps: "cap_net_bind_service=ep",
	}})
	if len(owned.Issues) != 1 || owned.Issues[0].Color != check.ColorRed || owned.Issues[0].Detail != "cap_net_bind_service=ep mode 0100755 user root group root" {
		t.Fatalf("owned %+v", owned)
	}
	unknown := m.Analyze([]check.File{{Path: "/usr/bin/odd", Kind: check.KindFile, Caps: "cap_net_broadcast=p"}})
	if len(unknown.Issues) != 1 || unknown.Issues[0].Color != check.ColorRed {
		t.Fatalf("unknown %+v", unknown)
	}
}
