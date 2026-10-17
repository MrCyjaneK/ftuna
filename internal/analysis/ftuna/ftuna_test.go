package ftuna

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func TestCheck(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, "github.com", "example", "app", "overlay", ".sfosbuild", "image")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".gitkeep", "23-rust.sh", "20-tdlib.sh", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clean := filepath.Join(root, "github.com", "example", "clean", "overlay")
	if err := os.MkdirAll(clean, 0o755); err != nil {
		t.Fatal(err)
	}

	appKey := "repo/ftuna-oss/5.1.0.11/aarch64/github.com/example/app/app-1.aarch64.rpm"
	got := Check(appKey, root)
	if len(got) != 2 || got[0].Code != "ftuna.built" || got[0].Detail != "hooks" || got[0].Color != check.ColorRed || got[0].Module != "ftuna" {
		t.Fatalf("%+v", got)
	}
	if got[1].Code != "ftuna.image" || got[1].Color != check.ColorRed || got[1].Detail != "20-tdlib.sh\n23-rust.sh\nnotes.txt" {
		t.Fatalf("%+v", got[1])
	}
	if got[0].Path != "github.com/example/app" {
		t.Fatalf("path %s", got[0].Path)
	}

	cleanKey := "scratch/20260102T150405Z-abcd/aarch64/github.com/example/clean/app-1.aarch64.rpm"
	got = Check(cleanKey, root)
	if len(got) != 1 || got[0].Code != "ftuna.built" || got[0].Detail != "clean" || got[0].Color != check.ColorGreen {
		t.Fatalf("%+v", got)
	}

	if got := Check("mirror/chum/obs/app.rpm", root); got != nil {
		t.Fatalf("%+v", got)
	}
	missing := "repo/ftuna-oss/5.1.0.11/aarch64/github.com/missing/app/app.rpm"
	got = Check(missing, root)
	if len(got) != 1 || got[0].Detail != "unknown" || got[0].Color != check.ColorYellow {
		t.Fatalf("%+v", got)
	}
	escaped := "repo/ftuna-oss/5.1.0.11/aarch64/../../secret/app.rpm"
	got = Check(escaped, root)
	if len(got) != 1 || got[0].Detail != "unknown" || got[0].Color != check.ColorYellow || got[0].Path != escaped {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "secret")); !os.IsNotExist(err) {
		t.Fatal("path escaped recipes root")
	}
}
