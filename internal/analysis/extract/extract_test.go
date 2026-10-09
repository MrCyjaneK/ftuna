package extract

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestDirNames(t *testing.T) {
	text := "Index of /\n\n../\n5.1.0.11/\ninfo.json.xz  12 KB\n"
	dirs := dirNames(text)
	if len(dirs) != 1 || dirs[0] != "5.1.0.11" {
		t.Fatalf("%#v", dirs)
	}
	html := `<a href="../">Parent</a><a href="5.1_aarch64/">5.1_aarch64/</a><a href="?C=N;O=D">Name</a><a href="notes.txt">notes</a>`
	dirs = dirNames(html)
	if len(dirs) != 1 || dirs[0] != "5.1_aarch64" {
		t.Fatalf("%#v", dirs)
	}
}

func TestJoinRepo(t *testing.T) {
	got, err := joinRepo("https://example.test/repo/", "a b.rpm")
	if err != nil || got != "https://example.test/repo/a%20b.rpm" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := joinRepo("https://example.test/repo/", "../secret.rpm"); err == nil {
		t.Fatal("expected bad href")
	}
}

func TestPrimaryPrefersGzip(t *testing.T) {
	got, err := primaryData([]repoData{
		{Type: "primary", Href: "repodata/a.xml.zst", Checksum: "aa", ChecksumType: "sha256"},
		{Type: "primary", Href: "repodata/b.xml.gz", Checksum: "bb", ChecksumType: "sha256"},
	})
	if err != nil || got.Href != "repodata/b.xml.gz" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestReleaseCurrent(t *testing.T) {
	keep := []string{"5.1_aarch64", "5.1_armv7hl", "5.1.0.11_i486", "5.2_aarch64", "6.0_aarch64"}
	drop := []string{"5.0_aarch64", "4.6_aarch64", "4.4.0.64_i486", "3.4.0.24_armv7hl", "not-a-repo"}
	for _, name := range keep {
		if !releaseCurrent(name) {
			t.Fatalf("excluded %s", name)
		}
	}
	for _, name := range drop {
		if releaseCurrent(name) {
			t.Fatalf("included %s", name)
		}
	}
}

func TestCollectSources(t *testing.T) {
	const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	repomd, gz := repoFiles(t, sum)

	t.Run("chum", func(t *testing.T) {
		srv := serve(t, map[string][]byte{
			"/":                                      []byte(`<a href="../">Parent</a><a href="5.1_aarch64/">5.1_aarch64/</a><a href="not-a-repo/">nope</a><a href="4.4.0.64_i486/">old</a>`),
			"/5.1_aarch64/repodata/repomd.xml":       repomd,
			"/5.1_aarch64/repodata/primary.xml.gz":   gz,
			"/4.4.0.64_i486/repodata/repomd.xml":     repomd,
			"/4.4.0.64_i486/repodata/primary.xml.gz": gz,
		})
		pkgs, err := Collect(context.Background(), srv.Client(), Chum, srv.URL+"/")
		if err != nil {
			t.Fatal(err)
		}
		if len(pkgs) != 2 {
			t.Fatalf("%d packages", len(pkgs))
		}
		if pkgs[0].Key != "chum/5.1_aarch64/a b.rpm" || !strings.HasSuffix(pkgs[0].URL, "/5.1_aarch64/a%20b.rpm") {
			t.Fatalf("%+v", pkgs[0])
		}
		for _, pkg := range pkgs {
			if strings.Contains(pkg.Key, "4.4.0.64") {
				t.Fatalf("old release %s", pkg.Key)
			}
		}
		if pkgs[0].SumType != "sha256" || pkgs[0].Sum != sum || pkgs[0].Size != 4 {
			t.Fatalf("%+v", pkgs[0])
		}
	})

	t.Run("openrepos", func(t *testing.T) {
		srv := serve(t, map[string][]byte{
			"/": []byte("Index of /\n\npiggz/\nopenrepos.key  1 KB\n"),
			"/piggz/personal/main/repodata/repomd.xml":     repomd,
			"/piggz/personal/main/repodata/primary.xml.gz": gz,
		})
		pkgs, err := Collect(context.Background(), srv.Client(), OpenRepos, srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkgs) != 2 || pkgs[1].Key != "mirror/openrepos/piggz/personal/main/github.com/example/app/demo.rpm" {
			t.Fatalf("%+v", pkgs)
		}
	})

	t.Run("oss", func(t *testing.T) {
		srv := serve(t, map[string][]byte{
			"/":                                     []byte("Index of /\n\n5.1.0.11/\ninfo.json.xz  12 KB\n"),
			"/5.1.0.11/":                            []byte("Index of /5.1.0.11/\n\n../\naarch64/\nnotes/\nmanifest.json  10 B\n"),
			"/5.1.0.11/aarch64/repodata/repomd.xml": repomd,
			"/5.1.0.11/aarch64/repodata/primary.xml.gz": gz,
		})
		pkgs, err := Collect(context.Background(), srv.Client(), OSS, srv.URL+"/")
		if err != nil {
			t.Fatal(err)
		}
		if len(pkgs) != 2 {
			t.Fatalf("%d packages", len(pkgs))
		}
		if pkgs[1].Key != "repo/ftuna-oss/5.1.0.11/aarch64/github.com/example/app/demo.rpm" {
			t.Fatalf("%+v", pkgs[1])
		}
		if !strings.Contains(pkgs[1].URL, "/5.1.0.11/aarch64/github.com/example/app/demo.rpm") {
			t.Fatalf("%s", pkgs[1].URL)
		}
	})
}

func TestPrimaryChecksum(t *testing.T) {
	const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	repomd, gz := repoFiles(t, sum)
	gz[len(gz)-1] ^= 0xff
	srv := serve(t, map[string][]byte{
		"/":                                     []byte("5.1.0.11/\n"),
		"/5.1.0.11/":                            []byte("aarch64/\n"),
		"/5.1.0.11/aarch64/repodata/repomd.xml": repomd,
		"/5.1.0.11/aarch64/repodata/primary.xml.gz": gz,
	})
	if _, err := Collect(context.Background(), srv.Client(), OSS, srv.URL+"/"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestCatalogSHA256(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const other = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	doc := infoJSON(sha, "y/demo-1.0-1.aarch64.rpm")
	srv := serve(t, map[string][]byte{
		"/info.json.xz": xzBytes(t, doc),
	})
	pkgs := []Package{{
		Key:     "mirror/openrepos/piggz/personal/main/y/demo-1.0-1.aarch64.rpm",
		URL:     srv.URL + "/piggz/personal/main/y/demo-1.0-1.aarch64.rpm",
		Sum:     strings.Repeat("ab", 20),
		SumType: "sha1",
		Size:    4,
	}, {
		Key:     "mirror/openrepos/piggz/personal/main/z/other-1.0-1.aarch64.rpm",
		URL:     srv.URL + "/piggz/personal/main/z/other-1.0-1.aarch64.rpm",
		Sum:     strings.Repeat("cd", 20),
		SumType: "sha1",
	}}
	sums, err := readInfo(context.Background(), srv.Client(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := overlayInfo(sums, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SumType != "sha256" || got[0].Sum != sha {
		t.Fatalf("%+v", got[0])
	}
	if got[1].SumType != "sha1" || got[1].Sum != pkgs[1].Sum {
		t.Fatalf("%+v", got[1])
	}
	if pkgs[0].SumType != "sha1" {
		t.Fatal("overlay changed the input slice")
	}

	conflict := []byte(`{"repositories":[{"id":"openrepos-piggz","url":"https://repo-openrepos.ftuna.eu/piggz/personal/main/"}],"packages":[` +
		`{"repo":"openrepos-piggz","rpms":[{"name":"demo","version":"","release":"","arch":"aarch64","sha256":"` + sha + `","href":"y/demo-1.0-1.aarch64.rpm"}]},` +
		`{"repo":"openrepos-piggz","rpms":[{"name":"demo","version":"","release":"","arch":"aarch64","sha256":"` + other + `","href":"y/demo-1.0-1.aarch64.rpm"}]}]}`)
	var parsed infoDoc
	if err := json.Unmarshal(conflict, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, err := indexInfo(parsed); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected one path with two hashes, got %v", err)
	}
	blank := []byte(`{"repositories":[{"id":"ftuna-oss-5.1.0.11-aarch64","url":"https://repo-oss.ftuna.eu/5.1.0.11/aarch64/"}],"packages":[` +
		`{"repo":"ftuna-oss-5.1.0.11-aarch64","rpms":[{"name":"sailfish-components-gallery-qt5","version":"","release":"","arch":"aarch64","sha256":"` + sha + `"}]}]}`)
	var noPath infoDoc
	if err := json.Unmarshal(blank, &noPath); err != nil {
		t.Fatal(err)
	}
	sums, err = indexInfo(noPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 0 {
		t.Fatalf("blank version was turned into a path: %#v", sums)
	}
}

func TestOpenReposHeaderTwinsStay(t *testing.T) {
	const a = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const b = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	raw := []byte(`{"repositories":[{"id":"openrepos-247","url":"https://repo-openrepos.ftuna.eu/247/personal/main/"}],
		"packages":[{"repo":"openrepos-247","rpms":[
			{"name":"ambience-tardis","version":"0.1","release":"1","arch":"armv7hl","sha256":"` + a + `"},
			{"name":"ambience-tardis","version":"0.1","release":"1","arch":"armv7hl","sha256":"` + b + `"},
			{"name":"ambience-tardis","version":"0.1","release":"1","arch":"armv7hl","sha256":"` + a + `"}
		]}]}`)
	var doc infoDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	pkgs, err := openreposFromInfo("https://repo-openrepos.ftuna.eu/", doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 || pkgs[0].Key == pkgs[1].Key || pkgs[0].URL != "" || pkgs[1].URL != "" {
		t.Fatalf("%+v", pkgs)
	}
	if pkgs[0].Key != "mirror/openrepos/247/"+pkgs[0].Sum || pkgs[1].Key != "mirror/openrepos/247/"+pkgs[1].Sum {
		t.Fatalf("%+v", pkgs)
	}

	conflict := []byte(`{"repositories":[{"id":"openrepos-247","url":"https://repo-openrepos.ftuna.eu/247/personal/main/"}],
		"packages":[{"repo":"openrepos-247","rpms":[
			{"name":"ambience-tardis","version":"0.1","release":"1","arch":"armv7hl","sha256":"` + a + `","href":"a/ambience-tardis-0.1-1.armv7hl.rpm"},
			{"name":"ambience-tardis","version":"0.1","release":"1","arch":"armv7hl","sha256":"` + b + `","href":"a/ambience-tardis-0.1-1.armv7hl.rpm"}
		]}]}`)
	if err := json.Unmarshal(conflict, &doc); err != nil {
		t.Fatal(err)
	}
	if _, err := openreposFromInfo("https://repo-openrepos.ftuna.eu/", doc); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected one path with two hashes, got %v", err)
	}
}

func TestCollectUsesCatalogSHA256(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var metaHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "repodata") || strings.HasSuffix(r.URL.Path, ".rpm") {
			metaHits++
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/info.json.xz" {
			_, _ = w.Write(xzBytes(t, infoJSON(sha, "y/demo-1.0-1.aarch64.rpm")))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	pkgs, err := Collect(context.Background(), srv.Client(), OpenRepos, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if metaHits != 0 {
		t.Fatalf("fetched rpm-md for a catalog listing: hits %d", metaHits)
	}
	if len(pkgs) != 1 || pkgs[0].SumType != "sha256" || pkgs[0].Sum != sha {
		t.Fatalf("%+v", pkgs)
	}
	if pkgs[0].Key != "mirror/openrepos/piggz/personal/main/y/demo-1.0-1.aarch64.rpm" {
		t.Fatalf("%s", pkgs[0].Key)
	}
	if pkgs[0].URL != srv.URL+"/piggz/personal/main/y/demo-1.0-1.aarch64.rpm" {
		t.Fatalf("%s", pkgs[0].URL)
	}
}

func TestFillLocatesMissingHref(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	repomd, gz := sha1Repo(t)
	var metaHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info.json.xz":
			_, _ = w.Write(xzBytes(t, infoJSON(sha, "")))
		case "/piggz/personal/main/repodata/repomd.xml":
			metaHits++
			_, _ = w.Write(repomd)
		case "/piggz/personal/main/repodata/primary.xml.gz":
			metaHits++
			_, _ = w.Write(gz)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	pkgs, err := Collect(context.Background(), srv.Client(), OpenRepos, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if metaHits != 0 || len(pkgs) != 1 || pkgs[0].URL != "" {
		t.Fatalf("hits %d %+v", metaHits, pkgs)
	}
	cache := &RepoCache{}
	got, err := cache.Fill(context.Background(), srv.Client(), pkgs[0])
	if err != nil {
		t.Fatal(err)
	}
	wantKey := "mirror/openrepos/piggz/personal/main/y/demo-1.0-1.aarch64.rpm"
	if got.URL != srv.URL+"/piggz/personal/main/y/demo-1.0-1.aarch64.rpm" || got.Key != wantKey {
		t.Fatalf("%+v", got)
	}
	if _, err := cache.Fill(context.Background(), srv.Client(), pkgs[0]); err != nil {
		t.Fatal(err)
	}
	if metaHits != 2 {
		t.Fatalf("primary reads %d", metaHits)
	}
}

func infoJSON(sha, href string) []byte {
	extra := ""
	if href != "" {
		extra = `,"href":"` + href + `"`
	}
	return fmt.Appendf(nil, `{
		"repositories":[{"id":"openrepos-piggz","url":"https://repo-openrepos.ftuna.eu/piggz/personal/main/"}],
		"packages":[{"repo":"openrepos-piggz","rpms":[
			{"name":"demo","version":"1.0","release":"1","arch":"aarch64","sha256":"%s"%s}
		]}]
	}`, sha, extra)
}

func sha1Repo(t *testing.T) (repomd, gz []byte) {
	t.Helper()
	xmlDoc := `<?xml version="1.0" encoding="UTF-8"?>
<metadata packages="1">
<package type="rpm"><name>demo</name><arch>aarch64</arch>
<version epoch="0" ver="1.0" rel="1"/>
<checksum type="sha" pkgid="YES">0123456789abcdef0123456789abcdef01234567</checksum>
<size package="4" installed="4" archive="4"/>
<location href="y/demo-1.0-1.aarch64.rpm"/>
</package>
</metadata>`
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(xmlDoc)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	gz = buf.Bytes()
	fileSum := sha256.Sum256(gz)
	repomd = fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<repomd>
  <data type="primary">
    <location href="repodata/primary.xml.gz"/>
    <checksum type="sha256">%s</checksum>
  </data>
</repomd>`, hex.EncodeToString(fileSum[:]))
	return repomd, gz
}

func xzBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func repoFiles(t *testing.T, sum string) (repomd, gz []byte) {
	t.Helper()
	xmlDoc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<metadata packages="3">
<package type="rpm"><name>demo</name><arch>aarch64</arch>
<checksum type="sha256" pkgid="YES">%s</checksum>
<size package="4" installed="4" archive="4"/>
<location href="github.com/example/app/demo.rpm"/>
</package>
<package type="rpm"><name>spaced</name><arch>noarch</arch>
<checksum type="sha256" pkgid="YES">%s</checksum>
<size package="4" installed="4" archive="4"/>
<location href="a b.rpm"/>
</package>
<package type="rpm"><name>src</name><arch>src</arch>
<checksum type="sha256" pkgid="YES">%s</checksum>
<size package="4" installed="4" archive="4"/>
<location href="demo.src.rpm"/>
</package>
</metadata>`, sum, sum, sum)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(xmlDoc)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	gz = buf.Bytes()
	fileSum := sha256.Sum256(gz)
	repomd = fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<repomd>
  <data type="primary">
    <location href="repodata/primary.xml.gz"/>
    <checksum type="sha256">%s</checksum>
  </data>
</repomd>`, hex.EncodeToString(fileSum[:]))
	return repomd, gz
}

func serve(t *testing.T, paths map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := paths[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}
