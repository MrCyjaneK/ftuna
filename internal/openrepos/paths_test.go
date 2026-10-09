package openrepos

import "testing"

func TestSafeRel(t *testing.T) {
	ok := []string{
		"r/reversegearhead-1.aarch64.rpm",
		"repodata/primary.xml.gz",
		"repodata/repomd.xml",
	}
	for _, href := range ok {
		got, err := safeRel(href)
		if err != nil {
			t.Fatalf("safeRel(%q): %v", href, err)
		}
		if got != href {
			t.Fatalf("safeRel(%q) = %q", href, got)
		}
	}
	bad := []string{
		"",
		"/etc/passwd",
		"../secret",
		"r/../../x",
		"r//foo.rpm",
		"r/foo.rpm/",
		"http://example/a.rpm",
		`r\foo.rpm`,
		"r/foo?.rpm",
	}
	for _, href := range bad {
		if _, err := safeRel(href); err == nil {
			t.Fatalf("safeRel(%q) accepted", href)
		}
	}
}
