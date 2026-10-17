package scan

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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/cron"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/dbus"
	"github.com/mrcyjanek/ftuna/internal/analysis/extract"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/filecaps"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/hooks"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/polkit"
	"github.com/mrcyjanek/ftuna/internal/analysis/rpmsample"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/sailjail"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/scriptlet"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/setuid"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/systemd"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/udev"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/worldwrite"
)

func TestParseArgs(t *testing.T) {
	if _, err := ParseArgs(nil); err == nil {
		t.Fatal("expected missing dir")
	}
	got, err := ParseArgs([]string{"/tmp/ratings"})
	if err != nil || got.RatingsDir != "/tmp/ratings" || len(got.Sources) != 3 || got.Sources[0] != extract.Chum || got.Sources[2] != extract.OSS {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = ParseArgs([]string{"/tmp/ratings", "oss", "chum", "oss"})
	if err != nil || got.RatingsDir != "/tmp/ratings" || len(got.Sources) != 2 || got.Sources[0] != extract.OSS || got.Sources[1] != extract.Chum {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ParseArgs([]string{"/tmp/ratings", "--prefix"}); err == nil {
		t.Fatal("expected unknown flag")
	}
	if _, err := ParseArgs([]string{"/tmp/ratings", "repo/ftuna-oss/"}); err == nil {
		t.Fatal("expected unexpected argument")
	}
}

func TestAnalyzeSample(t *testing.T) {
	body, err := rpmfileSample()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Analyze("repo/ftuna-oss/5.1.0.11/aarch64/demo.rpm", "abc", body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "demo" || doc.RPMVersion != "1.2.3" || doc.Key != "repo/ftuna-oss/5.1.0.11/aarch64/demo.rpm" {
		t.Fatalf("%+v", doc)
	}
	codes := map[string]int{}
	for _, issue := range doc.Issues {
		codes[issue.Module+" "+issue.Code]++
		if issue.Module == "" {
			t.Fatalf("missing module %+v", issue)
		}
	}
	want := map[string]int{
		"setuid setuid":                2,
		"setuid setgid":                2,
		"sailjail sailjail.permission": 1,
		"systemd systemd.service":      1,
		"ftuna ftuna.built":            1,
	}
	byColor := map[string]string{}
	for _, issue := range doc.Issues {
		byColor[issue.Module+" "+issue.Code] = issue.Color
	}
	if byColor["setuid setuid"] != check.ColorRed || byColor["sailjail sailjail.permission"] != check.ColorGray || byColor["systemd systemd.service"] != check.ColorRed || byColor["scriptlet scriptlet"] != check.ColorRed || byColor["ftuna ftuna.built"] != check.ColorYellow {
		t.Fatalf("colors %v", byColor)
	}
	for key, n := range want {
		if codes[key] != n {
			t.Fatalf("%s = %d, issues %+v", key, codes[key], doc.Issues)
		}
	}
	for _, issue := range doc.Issues {
		if issue.Path == "/usr/lib/systemd/system/multi-user.target.wants/demo.service" ||
			issue.Path == "/usr/share/sailjail/permissions/Internet.permission" ||
			issue.Code == "sailjail.absent" ||
			issue.Code == "ftuna.image" {
			t.Fatalf("unexpected %+v", issue)
		}
		if issue.Code == "ftuna.built" && issue.Detail != "unknown" {
			t.Fatalf("%+v", issue)
		}
		if issue.Code == "systemd.service" && issue.Detail != "scope=system user=root group=root exec=/usr/bin/demo sandbox=none" {
			t.Fatalf("%+v", issue)
		}
	}
}

func TestAnalyzeUnreadable(t *testing.T) {
	doc, err := Analyze("mirror/openrepos/aviarus/personal/main/o/okb-lang-fi-0.1-0.noarch.rpm", "abc", []byte("not an rpm"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Key == "" || len(doc.Issues) != 1 || doc.Issues[0].Code != "rpm.read" || doc.Issues[0].Detail == "" || doc.Issues[0].Color != check.ColorGray {
		t.Fatalf("%+v", doc)
	}
	if doc.Issues[0].Module != "rpm" || doc.Issues[0].Path != doc.Key {
		t.Fatalf("%+v", doc.Issues[0])
	}
}

func TestRunSkipMismatchAndWrite(t *testing.T) {
	body, err := rpmfileSample()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	var published string
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte("5.1.0.11/\n"))
		case "/5.1.0.11/":
			_, _ = w.Write([]byte("aarch64/\n"))
		case "/5.1.0.11/aarch64/repodata/repomd.xml":
			repomd, _ := ossMeta(t, published, body)
			_, _ = w.Write(repomd)
		case "/5.1.0.11/aarch64/repodata/primary.xml.gz":
			_, gz := ossMeta(t, published, body)
			_, _ = w.Write(gz)
		case "/5.1.0.11/aarch64/demo.rpm":
			hits++
			if r.Header.Get("Accept-Encoding") != "identity" {
				http.Error(w, "want identity", http.StatusBadRequest)
				return
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := func(dir string) Config {
		return Config{Sources: []string{extract.OSS}, RatingsDir: dir, CacheDir: t.TempDir(), OSS: srv.URL + "/"}
	}

	published = sha
	skipDir := t.TempDir()
	prior, err := check.Encode(check.Result{Version: check.Version, SHA256: sha})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeResult(skipDir, sha, prior); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), cfg(skipDir)); err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Fatalf("hits %d", hits)
	}

	published = strings.Repeat("ab", 32)
	hits = 0
	if err := Run(context.Background(), cfg(t.TempDir())); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	if hits != 1 {
		t.Fatalf("hits %d", hits)
	}

	published = sha
	freshDir := t.TempDir()
	hits = 0
	if err := Run(context.Background(), cfg(freshDir)); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("hits %d", hits)
	}
	raw, err := os.ReadFile(filepath.Join(freshDir, filepath.FromSlash(check.ObjectKey(sha))))
	if err != nil {
		t.Fatal(err)
	}
	var doc check.Result
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SHA256 != sha || doc.Version != check.Version || doc.Key != "repo/ftuna-oss/5.1.0.11/aarch64/demo.rpm" || len(doc.Issues) == 0 {
		t.Fatalf("%+v", doc)
	}
}

func ossMeta(t *testing.T, sum string, rpm []byte) (repomd, gz []byte) {
	t.Helper()
	xmlDoc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<metadata packages="2">
<package type="rpm"><name>demo</name><arch>aarch64</arch>
<checksum type="sha256" pkgid="YES">%s</checksum>
<size package="%d" installed="1" archive="1"/>
<location href="demo.rpm"/>
</package>
<package type="rpm"><name>src</name><arch>src</arch>
<checksum type="sha256" pkgid="YES">%s</checksum>
<size package="1" installed="1" archive="1"/>
<location href="demo.src.rpm"/>
</package>
</metadata>`, sum, len(rpm), sum)
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

func TestRunRefusesDownloadWithoutSHA256(t *testing.T) {
	body, err := rpmfileSample()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	repomd, gz := openRepoMeta(t, len(body))
	catalog := xzCatalog(t, sha)
	var rpmHits, metaHits int
	var withCatalog bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte("piggz/\n"))
		case "/info.json.xz":
			if !withCatalog {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(catalog)
		case "/piggz/personal/main/repodata/repomd.xml":
			metaHits++
			_, _ = w.Write(repomd)
		case "/piggz/personal/main/repodata/primary.xml.gz":
			metaHits++
			_, _ = w.Write(gz)
		case "/piggz/personal/main/y/demo-1.0-1.aarch64.rpm":
			rpmHits++
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := func(dir string) Config {
		return Config{Sources: []string{extract.OpenRepos}, RatingsDir: dir, CacheDir: t.TempDir(), OpenRepos: srv.URL + "/"}
	}

	if err := Run(context.Background(), cfg(t.TempDir())); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected missing sha256, got %v", err)
	}
	if rpmHits != 0 {
		t.Fatalf("downloaded without a sha256: hits %d", rpmHits)
	}

	withCatalog = true
	skipDir := t.TempDir()
	prior, err := check.Encode(check.Result{Version: check.Version, SHA256: sha})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeResult(skipDir, sha, prior); err != nil {
		t.Fatal(err)
	}
	rpmHits = 0
	metaHits = 0
	if err := Run(context.Background(), cfg(skipDir)); err != nil {
		t.Fatal(err)
	}
	if rpmHits != 0 || metaHits != 0 {
		t.Fatalf("stored package fetched rpm %d metadata %d", rpmHits, metaHits)
	}

	freshDir := t.TempDir()
	rpmHits = 0
	metaHits = 0
	if err := Run(context.Background(), cfg(freshDir)); err != nil {
		t.Fatal(err)
	}
	if rpmHits != 1 {
		t.Fatalf("hits %d", rpmHits)
	}
	raw, err := os.ReadFile(filepath.Join(freshDir, filepath.FromSlash(check.ObjectKey(sha))))
	if err != nil {
		t.Fatal(err)
	}
	var doc check.Result
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SHA256 != sha || doc.Key != "mirror/openrepos/piggz/personal/main/y/demo-1.0-1.aarch64.rpm" {
		t.Fatalf("%+v", doc)
	}
}

func openRepoMeta(t *testing.T, rpmLen int) (repomd, gz []byte) {
	t.Helper()
	xmlDoc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<metadata packages="1">
<package type="rpm"><name>demo</name><arch>aarch64</arch>
<version epoch="0" ver="1.0" rel="1"/>
<checksum type="sha" pkgid="YES">0123456789abcdef0123456789abcdef01234567</checksum>
<size package="%d" installed="1" archive="1"/>
<location href="y/demo-1.0-1.aarch64.rpm"/>
</package>
</metadata>`, rpmLen)
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

func xzCatalog(t *testing.T, sha string) []byte {
	t.Helper()
	raw := fmt.Appendf(nil, `{
		"repositories":[{"id":"openrepos-piggz","url":"https://repo-openrepos.ftuna.eu/piggz/personal/main/"}],
		"packages":[{"repo":"openrepos-piggz","rpms":[
			{"name":"demo","version":"1.0","release":"1","arch":"aarch64","sha256":"%s"}
		]}]
	}`, sha)
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

func TestRPMCache(t *testing.T) {
	body, err := rpmfileSample()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	cache := t.TempDir()
	keep := filepath.Join(cache, strings.Repeat("ab", 32))
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	var block bool
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte("5.1.0.11/\n"))
		case "/5.1.0.11/":
			_, _ = w.Write([]byte("aarch64/\n"))
		case "/5.1.0.11/aarch64/repodata/repomd.xml":
			repomd, _ := ossMeta(t, sha, body)
			_, _ = w.Write(repomd)
		case "/5.1.0.11/aarch64/repodata/primary.xml.gz":
			_, gz := ossMeta(t, sha, body)
			_, _ = w.Write(gz)
		case "/5.1.0.11/aarch64/demo.rpm":
			hits++
			if block {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := func(dir string) Config {
		return Config{Sources: []string{extract.OSS}, RatingsDir: dir, CacheDir: cache, OSS: srv.URL + "/"}
	}
	if err := Run(context.Background(), cfg(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("hits %d", hits)
	}
	cached, err := os.ReadFile(filepath.Join(cache, sha))
	if err != nil || !bytes.Equal(cached, body) {
		t.Fatalf("cache %v %d", err, len(cached))
	}

	block = true
	hits = 0
	if err := Run(context.Background(), cfg(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Fatalf("downloaded despite cache: %d", hits)
	}
	kept, err := os.ReadFile(keep)
	if err != nil || string(kept) != "keep" {
		t.Fatalf("cache entry removed: %v %q", err, kept)
	}

	if err := os.WriteFile(filepath.Join(cache, sha), []byte("not an rpm"), 0o644); err != nil {
		t.Fatal(err)
	}
	block = false
	hits = 0
	if err := Run(context.Background(), cfg(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("corrupt cache hits %d", hits)
	}
	cached, err = os.ReadFile(filepath.Join(cache, sha))
	if err != nil || !bytes.Equal(cached, body) {
		t.Fatal("corrupt cache was not replaced")
	}
	kept, err = os.ReadFile(keep)
	if err != nil || string(kept) != "keep" {
		t.Fatalf("unrelated cache entry removed: %v %q", err, kept)
	}
}

func TestRPMCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg"))
	got, err := rpmCacheDir("")
	if err != nil || got != filepath.Join(os.Getenv("XDG_CACHE_HOME"), "ftuna", "rpmcache") {
		t.Fatalf("%s %v", got, err)
	}
	got, err = rpmCacheDir(filepath.Join(t.TempDir(), "override"))
	if err != nil || !strings.HasSuffix(got, "override") {
		t.Fatalf("%s %v", got, err)
	}
}

func rpmfileSample() ([]byte, error) {
	return rpmsample.RPM()
}
