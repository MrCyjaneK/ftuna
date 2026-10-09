package scriptlet

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestScripts(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{Path: "%post", Kind: check.KindScriptlet, Program: "/bin/sh", Body: []byte("echo post\n")},
		{Path: "%postun", Kind: check.KindScriptlet, Program: "/sbin/ldconfig"},
		{Path: "%pre", Kind: check.KindScriptlet, Program: "/bin/sh", Body: []byte("# only a comment\n")},
		{Path: "/usr/bin/demo", Kind: check.KindFile, Body: []byte("echo no\n")},
	})
	if rep.Score != scoreRoot || len(rep.Issues) != 2 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	if rep.Issues[0].Detail != "user=root program=/bin/sh body=echo post" {
		t.Fatalf("post %+v", rep.Issues[0])
	}
	if rep.Issues[1].Path != "%postun" || rep.Issues[1].Detail != "user=root program=/sbin/ldconfig" {
		t.Fatalf("helper %+v", rep.Issues[1])
	}
	helper := m.Analyze([]check.File{{Path: "%post", Kind: check.KindScriptlet, Program: "/sbin/ldconfig"}})
	if helper.Score != scoreHelper {
		t.Fatalf("ldconfig %+v", helper)
	}
	odd := m.Analyze([]check.File{{Path: "%post", Kind: check.KindScriptlet, Program: "/usr/bin/demo"}})
	if odd.Score != scoreRoot || odd.Issues[0].Detail != "user=root program=/usr/bin/demo" {
		t.Fatalf("odd %+v", odd)
	}
	if got := m.Analyze(nil); got.Score != check.Neutral {
		t.Fatalf("none %+v", got)
	}
}
