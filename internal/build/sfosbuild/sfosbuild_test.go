package sfosbuild

import "testing"

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
