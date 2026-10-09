package udev

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestRun(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{
			Path: "/usr/lib/udev/rules.d/10-demo.rules",
			Kind: check.KindFile,
			Body: []byte("SUBSYSTEM==\"net\", ACTION==\"add\", RUN+=\"/usr/bin/demo\"\nKERNEL==\"demo\", RUN{builtin}+=\"kmod load demo\"\n"),
		},
		{
			Path: "/etc/udev/rules.d/20-mode.rules",
			Kind: check.KindFile,
			Body: []byte("KERNEL==\"demo\", MODE=\"0660\", OWNER=\"nemo\"\n"),
		},
		{
			Path: "/usr/lib/demo/not.rules",
			Kind: check.KindFile,
			Body: []byte("RUN+=\"/bin/sh\"\n"),
		},
	})
	if rep.Score != scoreProgram || len(rep.Issues) != 1 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	if rep.Issues[0].Detail != "user=root program=/usr/bin/demo" {
		t.Fatalf("%+v", rep.Issues)
	}
	only := m.Analyze([]check.File{{
		Path: "/lib/udev/rules.d/10-builtin.rules", Kind: check.KindFile,
		Body: []byte("ACTION==\"add\", RUN{builtin}+=\"hwdb\"\n"),
	}})
	if only.Score != check.Neutral || len(only.Issues) != 0 {
		t.Fatalf("%+v", only)
	}
	if got := m.Analyze(nil); got.Score != check.Neutral {
		t.Fatalf("none %+v", got)
	}
	extra := m.Analyze([]check.File{{
		Path: "/usr/lib/udev/rules.d/30-extra.rules", Kind: check.KindFile,
		Body: []byte("KERNEL==\"demo\", PROGRAM==\"/usr/bin/check\", IMPORT{program}=\"/usr/bin/import\", MODE=\"0666\"\nKERNEL==\"priv\", MODE=\"0660\"\n"),
	}})
	if extra.Score != scoreMode || len(extra.Issues) != 3 {
		t.Fatalf("extra %d %+v", extra.Score, extra.Issues)
	}
	if extra.Issues[0].Detail != "user=root program=/usr/bin/check" || extra.Issues[1].Detail != "user=root program=/usr/bin/import" || extra.Issues[2].Code != "udev.mode" || extra.Issues[2].Detail != "user=root mode=0666" {
		t.Fatalf("%+v", extra.Issues)
	}
}
