package dbus

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestServiceAndPolicy(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{
			Path: "/usr/share/dbus-1/system-services/org.demo.service",
			Kind: check.KindFile,
			Body: []byte("[D-BUS Service]\nName=org.demo\nExec=/usr/bin/demo\nUser=nemo\n"),
		},
		{
			Path: "/usr/share/dbus-1/services/org.demo.service",
			Kind: check.KindFile,
			Body: []byte("[D-BUS Service]\nExec=/usr/bin/demo\n"),
		},
		{
			Path: "/usr/share/dbus-1/system.d/org.demo.conf",
			Kind: check.KindFile,
			Body: []byte(`<busconfig>
			  <policy user="root"><allow own="org.demo"/></policy>
			  <policy context="default"><allow send_destination="org.demo"/></policy>
			</busconfig>`),
		},
		{
			Path: "/etc/dbus-1/system.d/wide.conf",
			Kind: check.KindFile,
			Body: []byte(`<busconfig><policy context="default"><allow own="*" eavesdrop="true"/></policy></busconfig>`),
		},
	})
	if rep.Score != scoreWild || len(rep.Issues) != 3 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	if rep.Issues[0].Code != "dbus.service" || rep.Issues[0].Detail != "scope=system user=nemo exec=/usr/bin/demo" {
		t.Fatalf("service %+v", rep.Issues[0])
	}
	if rep.Issues[1].Code != "dbus.service" || rep.Issues[1].Detail != "scope=session user=session exec=/usr/bin/demo" {
		t.Fatalf("session %+v", rep.Issues[1])
	}
	if rep.Issues[2].Code != "dbus.policy" || rep.Issues[2].Detail != "context=default eavesdrop context=default own=*" {
		t.Fatalf("policy %+v", rep.Issues[2])
	}

	root := m.Analyze([]check.File{{
		Path: "/usr/share/dbus-1/system-services/org.demo.service",
		Kind: check.KindFile,
		Body: []byte("[D-BUS Service]\nExec=/usr/bin/demo\n"),
	}})
	if root.Score != scoreRoot || root.Issues[0].Detail != "scope=system user=root exec=/usr/bin/demo" {
		t.Fatalf("root %+v", root)
	}
	own := m.Analyze([]check.File{{
		Path: "/etc/dbus-1/system.d/own.conf",
		Kind: check.KindFile,
		Body: []byte(`<busconfig><policy user="*"><allow own="org.demo"/></policy></busconfig>`),
	}})
	if own.Score != scoreOwn || own.Issues[0].Detail != "user=* own" {
		t.Fatalf("own %+v", own)
	}
	if got := m.Analyze(nil); got.Score != check.Neutral {
		t.Fatalf("none %+v", got)
	}
}
