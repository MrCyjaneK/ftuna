package deps

import (
	"strings"
	"testing"
)

func TestSelectSkipsPlatformAndFollowsOurs(t *testing.T) {
	built := []RPM{{
		File: "app-1.aarch64.rpm",
		Name: "app",
		Requires: []string{
			"sailfishsilica-qt5 >= 1.2.78",
			"libbaz >= 1",
			"rpmlib(CompressedFileNames) <= 3.0.4-1",
		},
	}}
	published := []RPM{
		{File: "libbaz-1.aarch64.rpm", Name: "libbaz", Requires: []string{"libqux"}},
		{File: "libqux-1.aarch64.rpm", Name: "libqux"},
	}
	our := map[string]struct{}{"app": {}, "libbaz": {}, "libqux": {}}
	got, err := Select(built, published, our)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "libbaz" || got[1].Name != "libqux" {
		t.Fatalf("got %#v", got)
	}
}

func TestSelectMissingArch(t *testing.T) {
	built := []RPM{{File: "app-1.aarch64.rpm", Name: "app", Requires: []string{"libfoo"}}}
	our := map[string]struct{}{"app": {}, "libfoo": {}}
	_, err := Select(built, nil, our)
	if err == nil || !strings.Contains(err.Error(), "libfoo") {
		t.Fatalf("error %v", err)
	}
}

func TestSelectSiblingAlreadyBuilt(t *testing.T) {
	built := []RPM{
		{File: "app-1.aarch64.rpm", Name: "app"},
		{File: "app-tests-1.aarch64.rpm", Name: "app-tests", Requires: []string{"app = 1"}},
	}
	our := map[string]struct{}{"app": {}, "app-tests": {}}
	got, err := Select(built, nil, our)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
