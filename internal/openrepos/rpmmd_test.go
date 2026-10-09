package openrepos

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleRepoMD = `<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo">
  <data type="primary">
    <location href="repodata/primary.xml.gz"/>
    <checksum type="sha">7535892df011b5dbaaab324f02e7232ba75dd9f3</checksum>
    <open-checksum type="sha">39933180a9bad07ee9174e9a8965cf049102bafc</open-checksum>
  </data>
</repomd>`

func TestParseRepoMD(t *testing.T) {
	md, err := parseRepoMD([]byte(sampleRepoMD))
	if err != nil {
		t.Fatal(err)
	}
	if len(md.Data) != 1 || md.Data[0].Href != "repodata/primary.xml.gz" || md.Data[0].ChecksumType != "sha" {
		t.Fatalf("%+v", md)
	}
}

func TestParsePrimary(t *testing.T) {
	xmlDoc := `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">
<package type="rpm"><name>demo</name><arch>aarch64</arch>
<version epoch="0" ver="1.0" rel="1"/>
<checksum type="sha" pkgid="YES">abc</checksum>
<size package="10" installed="20" archive="20"/>
<location href="d/demo-1.0-1.aarch64.rpm"/>
</package>
</metadata>`
	pkgs, err := parsePrimaryXML(strings.NewReader(xmlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "demo" || pkgs[0].Href != "d/demo-1.0-1.aarch64.rpm" || pkgs[0].Size != 10 || pkgs[0].Version != "1.0" {
		t.Fatalf("%+v", pkgs)
	}
}

func TestOpenSumReadsGzipAndBzip2(t *testing.T) {
	raw := []byte("hello metadata\n")
	dir := t.TempDir()

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	gzPath := filepath.Join(dir, "meta")
	if err := os.WriteFile(gzPath, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := openSum(gzPath, "repodata/other.xml.gz", "sha1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "f73000a45b551692717f22c0a57b5a6341427942" {
		t.Fatalf("gzip open sum %s", got)
	}

	bz2 := []byte{66, 90, 104, 57, 49, 65, 89, 38, 83, 89, 184, 55, 118, 129, 0, 0, 4, 81, 128, 0, 16, 64, 0, 38, 70, 132, 0, 32, 0, 34, 6, 141, 50, 16, 3, 11, 78, 52, 115, 41, 186, 35, 197, 220, 145, 78, 20, 36, 46, 13, 221, 160, 64}
	bzPath := filepath.Join(dir, "sqlite")
	if err := os.WriteFile(bzPath, bz2, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = openSum(bzPath, "repodata/other.sqlite.bz2", "sha1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "f73000a45b551692717f22c0a57b5a6341427942" {
		t.Fatalf("bzip2 open sum %s", got)
	}
}

func TestParsePrimaryRejectsAbsolute(t *testing.T) {
	xmlDoc := `<metadata xmlns="http://linux.duke.edu/metadata/common"><package type="rpm"><name>demo</name><location href="https://example/a.rpm"/></package></metadata>`
	if _, err := parsePrimaryXML(strings.NewReader(xmlDoc)); err == nil {
		t.Fatal("expected error")
	}
}
