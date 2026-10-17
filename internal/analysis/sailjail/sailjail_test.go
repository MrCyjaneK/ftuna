package sailjail

import (
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestDesktop(t *testing.T) {
	m := module{}
	perms := m.Analyze([]check.File{{
		Path: "/usr/share/applications/demo.desktop",
		Kind: check.KindFile,
		Body: []byte("[Desktop Entry]\nName=Demo\n\n[X-Sailjail]\nPermissions=Internet; Pictures;\n"),
	}})
	if len(perms.Issues) != 2 || perms.Issues[0].Code != "sailjail.permission" || perms.Issues[0].Color != check.ColorGray || perms.Issues[0].Detail != "Internet" || perms.Issues[1].Detail != "Pictures" || perms.Issues[1].Color != check.ColorGray {
		t.Fatalf("perms %+v", perms)
	}
	off := m.Analyze([]check.File{{
		Path: "/usr/share/applications/off.desktop",
		Kind: check.KindFile,
		Body: []byte("[X-Sailjail]\nSandboxing=disabled\nPermissions=Internet\n"),
	}})
	if len(off.Issues) != 2 || off.Issues[0].Code != "sailjail.disabled" || off.Issues[0].Color != check.ColorYellow || off.Issues[1].Code != "sailjail.permission" || off.Issues[1].Color != check.ColorGray {
		t.Fatalf("off %+v", off)
	}
	plain := m.Analyze([]check.File{{
		Path: "/usr/share/applications/plain.desktop",
		Kind: check.KindFile,
		Body: []byte("[Desktop Entry]\nName=Plain\n"),
	}})
	if len(plain.Issues) != 1 || plain.Issues[0].Code != "sailjail.absent" || plain.Issues[0].Color != check.ColorYellow {
		t.Fatalf("plain %+v", plain)
	}
	worst := m.Analyze([]check.File{
		{Path: "/usr/share/applications/demo.desktop", Kind: check.KindFile, Body: []byte("[X-Sailjail]\nPermissions=Internet\n")},
		{Path: "/usr/share/applications/plain.desktop", Kind: check.KindFile, Body: []byte("[Desktop Entry]\nName=Plain\n")},
	})
	if len(worst.Issues) != 2 || worst.Issues[0].Color != check.ColorGray || worst.Issues[1].Color != check.ColorYellow {
		t.Fatalf("worst %+v", worst)
	}
	clean := m.Analyze([]check.File{{
		Path: "/usr/share/applications/clean.desktop",
		Kind: check.KindFile,
		Body: []byte("[X-Sailjail]\nOrganizationName=org.demo\nApplicationName=Clean\n"),
	}})
	if len(clean.Issues) != 1 || clean.Issues[0].Code != "sailjail.jailed" || clean.Issues[0].Color != check.ColorGreen {
		t.Fatalf("clean %+v", clean)
	}
	skipped := []check.File{
		{Path: "/usr/share/applications/nested/app.desktop", Kind: check.KindFile, Body: []byte("[Desktop Entry]\n")},
		{Path: "/usr/share/sailjail/permissions/Internet.permission", Kind: check.KindFile, Body: []byte("Internet\n")},
		{Path: "/usr/share/applications/link.desktop", Kind: check.KindSymlink},
	}
	if got := m.Analyze(skipped); len(got.Issues) != 0 {
		t.Fatalf("skipped %+v", got)
	}
}
