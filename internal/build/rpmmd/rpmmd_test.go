package rpmmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/rpmpack"
)

func TestReadRequires(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app-1.0.0-1.aarch64.rpm")
	writeRPM(t, path, rpmpack.RPMMetaData{
		Name: "app", Version: "1.0.0", Release: "1", Arch: "aarch64", Summary: "An app",
		Requires: rpmpack.Relations{
			{Name: "sailfishsilica-qt5", Version: "1.2.78", Sense: rpmpack.SenseGreater | rpmpack.SenseEqual},
			{Name: "libbaz", Version: "1", Sense: rpmpack.SenseEqual},
		},
		Provides: rpmpack.Relations{
			{Name: "app(aarch-64)", Version: "1.0.0-1", Sense: rpmpack.SenseEqual},
		},
	}, []rpmpack.RPMFile{
		{Name: "/usr/bin/app", Body: []byte("app"), Mode: 0755, Owner: "root", Group: "root"},
	}, nil)
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.File != "app-1.0.0-1.aarch64.rpm" || got.Name != "app" || got.Version != "1.0.0" || got.Release != "1" || got.Summary != "An app" {
		t.Fatalf("%#v", got)
	}
	if len(got.Requires) != 2 || got.Requires[0] != "sailfishsilica-qt5 >= 1.2.78" || got.Requires[1] != "libbaz = 1" {
		t.Fatalf("requires %#v", got.Requires)
	}
	prov := map[string]bool{}
	for _, name := range got.Provides {
		prov[name] = true
	}
	if len(got.Provides) != 2 || !prov["app"] || !prov["app(aarch-64)"] {
		t.Fatalf("provides %#v", got.Provides)
	}
	if got.SHA256 == "" {
		t.Fatal("missing sha256")
	}
}

func TestWriteAndMerge(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app-1.0.0-1.aarch64.rpm")
	writeRPM(t, app, rpmpack.RPMMetaData{
		Name: "app", Version: "1.0.0", Release: "1", Arch: "aarch64",
		Summary: "An app", Description: "An application",
		Requires: rpmpack.Relations{
			{Name: "sailfishsilica-qt5", Version: "1.2.78", Sense: rpmpack.SenseGreater | rpmpack.SenseEqual},
		},
	}, []rpmpack.RPMFile{
		{Name: "/etc/app.conf", Body: []byte("conf"), Mode: 0644, Owner: "root", Group: "root", Type: rpmpack.ConfigFile},
		{Name: "/usr/bin/app", Body: []byte("app"), Mode: 0755, Owner: "root", Group: "root"},
		{Name: "/usr/lib/libapp.so", Body: []byte("so"), Mode: 0755, Owner: "root", Group: "root"},
		{Name: "/usr/share/app/readme.txt", Body: []byte("hi"), Mode: 0644, Owner: "root", Group: "root"},
		{Name: "/var/log/app.log", Mode: 0644, Owner: "root", Group: "root", Type: rpmpack.GhostFile},
	}, func(rpm *rpmpack.RPM) {
		rpm.AddCustomTag(1080, rpmpack.EntryInt32([]int32{1700000000}))
		rpm.AddCustomTag(1081, rpmpack.EntryStringSlice([]string{"Ada <ada@example.com> - 1.0.0-1"}))
		rpm.AddCustomTag(1082, rpmpack.EntryStringSlice([]string{"- first"}))
	})

	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	primary, lists, other := readRepo(t, dir)
	if !strings.Contains(primary, `flags="GE"`) || !strings.Contains(primary, `ver="1.2.78"`) {
		t.Fatalf("requires missing from primary: %s", primary)
	}
	if !strings.Contains(primary, "<file>/usr/bin/app</file>") || !strings.Contains(primary, "<file>/etc/app.conf</file>") || !strings.Contains(primary, "<file>/usr/lib/libapp.so</file>") {
		t.Fatalf("primary files: %s", primary)
	}
	if strings.Contains(primary, "readme.txt") {
		t.Fatalf("readme belongs in filelists only: %s", primary)
	}
	if !strings.Contains(primary, `type="ghost"`) || !strings.Contains(primary, "/var/log/app.log") {
		t.Fatalf("ghost missing: %s", primary)
	}
	if !strings.Contains(lists, "readme.txt") || !strings.Contains(lists, "/usr/bin/app") {
		t.Fatalf("filelists: %s", lists)
	}
	if !strings.Contains(other, "first") {
		t.Fatalf("changelog: %s", other)
	}
	checkRange(t, app, primary)

	pkgs, err := Split([]byte(primary), []byte(lists), []byte(other))
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	gotCache, err := CacheDir("5.1.0.11", "aarch64")
	if err != nil {
		t.Fatal(err)
	}
	if gotCache != filepath.Join(cache, "ftuna", "oss", "5.1.0.11", "aarch64") {
		t.Fatalf("cache %s", gotCache)
	}
	saved, ok := pkgs["app-1.0.0-1.aarch64.rpm"]
	if !ok {
		t.Fatalf("locations %v", keys(pkgs))
	}
	if err := SavePackage(gotCache, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(app); err != nil {
		t.Fatal(err)
	}

	dir2 := t.TempDir()
	lib := filepath.Join(dir2, "lib-2.0.0-1.aarch64.rpm")
	writeRPM(t, lib, rpmpack.RPMMetaData{
		Name: "lib", Version: "2.0.0", Release: "1", Arch: "aarch64", Summary: "A lib",
	}, []rpmpack.RPMFile{
		{Name: "/usr/lib/libdemo.so.1", Body: []byte("so"), Mode: 0755, Owner: "root", Group: "root"},
	}, nil)
	second, err := Inspect(lib, "lib-2.0.0-1.aarch64.rpm")
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := LoadPackage(gotCache, "app-1.0.0-1.aarch64.rpm")
	if err != nil || !ok {
		t.Fatalf("load ok=%v err=%v", ok, err)
	}
	if err := WritePackages(dir2, []Package{loaded, second}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "app-1.0.0-1.aarch64.rpm")); !os.IsNotExist(err) {
		t.Fatalf("merged repo should not need the first rpm: %v", err)
	}
	merged, _, _ := readRepo(t, dir2)
	if !strings.Contains(merged, ">app</name>") || !strings.Contains(merged, ">lib</name>") {
		t.Fatalf("merged primary: %s", merged)
	}
}

func writeRPM(t *testing.T, path string, meta rpmpack.RPMMetaData, files []rpmpack.RPMFile, tune func(*rpmpack.RPM)) {
	t.Helper()
	rpm, err := rpmpack.NewRPM(meta)
	if err != nil {
		t.Fatal(err)
	}
	if tune != nil {
		tune(rpm)
	}
	for _, file := range files {
		rpm.AddFile(file)
	}
	var buf bytes.Buffer
	if err := rpm.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func checkRange(t *testing.T, rpmPath, primary string) {
	t.Helper()
	var doc struct {
		Format struct {
			Range struct {
				Start int `xml:"start,attr"`
				End   int `xml:"end,attr"`
			} `xml:"header-range"`
		} `xml:"format"`
	}
	raw := primary[strings.Index(primary, "<package") : strings.Index(primary, "</package>")+len("</package>")]
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(rpmPath)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format.Range.Start <= 0 || doc.Format.Range.End <= doc.Format.Range.Start || doc.Format.Range.End > len(body) {
		t.Fatalf("header-range %d %d len %d", doc.Format.Range.Start, doc.Format.Range.End, len(body))
	}
	if body[doc.Format.Range.Start] != 0x8e || body[doc.Format.Range.Start+1] != 0xad {
		t.Fatalf("header magic %x", body[doc.Format.Range.Start:doc.Format.Range.Start+4])
	}
}

func readRepo(t *testing.T, dir string) (primary, lists, other string) {
	t.Helper()
	repo := filepath.Join(dir, "repodata")
	repomd, err := os.ReadFile(filepath.Join(repo, "repomd.xml"))
	if err != nil {
		t.Fatal(err)
	}
	ph, fh, oh, err := GzipHrefs(repomd)
	if err != nil {
		t.Fatal(err)
	}
	primary = openMeta(t, dir, ph, repomd)
	lists = openMeta(t, dir, fh, repomd)
	other = openMeta(t, dir, oh, repomd)
	return primary, lists, other
}

func openMeta(t *testing.T, dir, href string, repomd []byte) string {
	t.Helper()
	name := filepath.Base(href)
	if len(name) < 64 || name[64] != '-' {
		t.Fatalf("metadata name %s", name)
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(href)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != name[:64] {
		t.Fatalf("gzip name %s hashes to %x", name, sum)
	}
	plain, err := Gunzip(body)
	if err != nil {
		t.Fatal(err)
	}
	open := sha256.Sum256(plain)
	want := hex.EncodeToString(open[:])
	var doc struct {
		Data []struct {
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			OpenChecksum struct {
				Value string `xml:",chardata"`
			} `xml:"open-checksum"`
		} `xml:"data"`
	}
	if err := xml.Unmarshal(repomd, &doc); err != nil {
		t.Fatal(err)
	}
	for _, data := range doc.Data {
		if data.Location.Href != href {
			continue
		}
		if strings.TrimSpace(data.OpenChecksum.Value) != want {
			t.Fatalf("open-checksum %s want %s", data.OpenChecksum.Value, want)
		}
		return string(plain)
	}
	t.Fatalf("repomd missing %s", href)
	return ""
}

func keys(m map[string]Package) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
