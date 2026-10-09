package rpmfile

import (
	"bytes"
	"testing"

	"github.com/google/rpmpack"
	"github.com/mrcyjanek/ftuna/internal/analysis/check"
	"github.com/mrcyjanek/ftuna/internal/analysis/rpmsample"
	"github.com/sassoftware/go-rpmutils"
)

func TestReadPackage(t *testing.T) {
	rpm, err := rpmsample.RPM()
	if err != nil {
		t.Fatal(err)
	}
	id, files, err := Read(rpm)
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "demo" || id.Version != "1.2.3" || id.Release != "4" || id.Arch != "aarch64" {
		t.Fatalf("%+v", id)
	}
	byPath := map[string]check.File{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	bin := byPath["/usr/bin/demo"]
	if bin.Kind != check.KindFile || bin.Mode&04000 == 0 || bin.User != "root" || string(bin.Body) != "bin" {
		t.Fatalf("bin %+v", bin)
	}
	both := byPath["/usr/bin/both"]
	if both.Mode&04000 == 0 || both.Mode&02000 == 0 {
		t.Fatalf("both mode %#o", both.Mode)
	}
	if byPath["/usr/bin/helper"].Mode&02000 == 0 || byPath["/usr/bin/helper"].Mode&04000 != 0 {
		t.Fatalf("helper %#o", byPath["/usr/bin/helper"].Mode)
	}
	link := byPath["/usr/bin/link"]
	if link.Kind != check.KindSymlink || len(link.Body) != 0 {
		t.Fatalf("link %+v", link)
	}
	ghost := byPath["/var/log/demo.log"]
	if ghost.Kind != check.KindGhost || len(ghost.Body) != 0 {
		t.Fatalf("ghost %+v", ghost)
	}
	if byPath["/usr/share/demo"].Kind != check.KindDir {
		t.Fatalf("dir %+v", byPath["/usr/share/demo"])
	}
	if string(byPath["/usr/share/applications/demo.desktop"].Body) == "" {
		t.Fatal("desktop body missing")
	}
	post := byPath["%post"]
	if post.Kind != check.KindScriptlet || post.Program != "/bin/sh" || string(post.Body) != "echo post\n" {
		t.Fatalf("post %+v %q", post, post.Body)
	}
	if byPath["%pretrans"].Kind != check.KindScriptlet || string(byPath["%pretrans"].Body) != "echo pretrans\n" {
		t.Fatalf("pretrans %+v", byPath["%pretrans"])
	}
	if byPath["%verifyscript"].Program != "/bin/sh" {
		t.Fatalf("verify %+v", byPath["%verifyscript"])
	}
	trig := byPath["%triggerin glibc"]
	if trig.Kind != check.KindScriptlet || string(trig.Body) != "echo trigger\n" || trig.Program != "/bin/sh" {
		t.Fatalf("trigger %+v %q", trig, trig.Body)
	}
	ftrig := byPath["%filetriggerin /usr/share/applications"]
	if string(ftrig.Body) != "echo filetrigger\n" {
		t.Fatalf("filetrigger %+v %q", ftrig, ftrig.Body)
	}
	if byPath["/usr/lib/systemd/system/demo.service"].Kind != check.KindFile {
		t.Fatalf("unit %+v", byPath["/usr/lib/systemd/system/demo.service"])
	}
	if byPath["/usr/bin/demo"].Caps != "" {
		t.Fatalf("caps %q", byPath["/usr/bin/demo"].Caps)
	}
}

func TestFileCaps(t *testing.T) {
	meta := rpmpack.RPMMetaData{Name: "caps", Version: "1", Release: "1", Arch: "noarch"}
	rpm, err := rpmpack.NewRPM(meta)
	if err != nil {
		t.Fatal(err)
	}
	rpm.AddFile(rpmpack.RPMFile{Name: "/usr/bin/bind", Body: []byte("b"), Mode: 0755, Owner: "root", Group: "root"})
	rpm.AddFile(rpmpack.RPMFile{Name: "/usr/bin/admin", Body: []byte("a"), Mode: 0755, Owner: "root", Group: "root"})
	// Header order follows the sorted path: admin, then bind.
	rpm.AddCustomTag(rpmutils.FILECAPS, rpmpack.EntryStringSlice([]string{
		"cap_sys_admin=ep",
		"cap_net_bind_service=ep",
	}))
	var buf bytes.Buffer
	if err := rpm.Write(&buf); err != nil {
		t.Fatal(err)
	}
	_, files, err := Read(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range files {
		got[file.Path] = file.Caps
	}
	if got["/usr/bin/admin"] != "cap_sys_admin=ep" || got["/usr/bin/bind"] != "cap_net_bind_service=ep" {
		t.Fatalf("%v", got)
	}
}
