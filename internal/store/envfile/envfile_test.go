package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FTUNA_ENV_FILE=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FTUNA_ENV_FILE", "")
	os.Unsetenv("FTUNA_ENV_FILE")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FTUNA_ENV_FILE") != "1" {
		t.Fatalf("got %q", os.Getenv("FTUNA_ENV_FILE"))
	}
	if err := Load(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing file: %v", err)
	}
}
