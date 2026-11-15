package sfosbuild

import (
	"strings"
	"testing"
)

func TestCommandArgsStampBeforeVersion(t *testing.T) {
	args := commandArgs("/tmp/rpms", "5.1.0.11", "aarch64,armv7hl,i486", "rpm/app.yaml")
	got := strings.Join(args, " ")
	want := "-o /tmp/rpms --stamp 5.1.0.11 aarch64,armv7hl,i486 rpm/app.yaml"
	if got != want {
		t.Fatalf("args %q", got)
	}
}

func TestTailWriterKeepsEnd(t *testing.T) {
	w := &tailWriter{limit: 4}
	if _, err := w.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := w.String(); got != "cdef" {
		t.Fatalf("tail %q", got)
	}
	if _, err := w.Write([]byte("gh")); err != nil {
		t.Fatal(err)
	}
	if got := w.String(); got != "efgh" {
		t.Fatalf("tail %q", got)
	}
}
