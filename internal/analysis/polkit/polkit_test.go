package polkit

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestGrants(t *testing.T) {
	m := module{}
	rep := m.Analyze([]check.File{
		{
			Path: "/usr/share/polkit-1/actions/org.demo.policy",
			Kind: check.KindFile,
			Body: []byte(`<policyconfig>
			  <action id="org.demo.admin">
			    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>auth_admin</allow_active></defaults>
			  </action>
			  <action id="org.demo.open">
			    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>yes</allow_active></defaults>
			  </action>
			</policyconfig>`),
		},
		{
			Path: "/usr/share/polkit-1/rules.d/10-demo.rules",
			Kind: check.KindFile,
			Body: []byte("// return polkit.Result.YES\npolkit.addRule(function(){ return polkit.Result.YES; });\n"),
		},
		{
			Path: "/etc/polkit-1/localauthority/50-local.d/demo.pkla",
			Kind: check.KindFile,
			Body: []byte("[Demo]\nIdentity=unix-user:*\nResultActive=yes\nResultAny=no\n"),
		},
		{
			Path: "/usr/share/polkit-1/rules.d/20-admin.rules",
			Kind: check.KindFile,
			Body: []byte("return polkit.Result.AUTH_ADMIN;\n"),
		},
	})
	if rep.Score != scoreAny || len(rep.Issues) != 3 {
		t.Fatalf("%d %+v", rep.Score, rep.Issues)
	}
	if rep.Issues[0].Code != "polkit.policy" || rep.Issues[0].Detail != "org.demo.open allow_active=yes" {
		t.Fatalf("policy %+v", rep.Issues[0])
	}
	if rep.Issues[1].Detail != "polkit.addRule(function(){ return polkit.Result.YES; });" {
		t.Fatalf("rules %+v", rep.Issues[1])
	}
	if rep.Issues[2].Code != "polkit.pkla" || rep.Issues[2].Detail != "Identity=unix-user:* ResultActive=yes" {
		t.Fatalf("pkla %+v", rep.Issues[2])
	}

	any := m.Analyze([]check.File{{
		Path: "/var/lib/polkit-1/localauthority/10-vendor.d/demo.pkla",
		Kind: check.KindFile,
		Body: []byte("[Demo]\nResultAny=yes\nResultActive=yes\n"),
	}})
	if any.Score != scoreAny || any.Issues[0].Code != "polkit.pkla" || any.Issues[0].Detail != "ResultAny=yes" {
		t.Fatalf("pkla %+v", any)
	}
	if got := m.Analyze(nil); got.Score != check.Neutral {
		t.Fatalf("none %+v", got)
	}
}
