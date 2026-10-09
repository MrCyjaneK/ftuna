package scratch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPublishAsChoosesID(t *testing.T) {
	const account = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const id = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("tus-resumable", "1.0.0")
		if r.Method == http.MethodPost {
			if r.Header.Get("x-ftuna-id") != id {
				t.Errorf("id %q", r.Header.Get("x-ftuna-id"))
			}
			w.Header().Set("location", "/v1/repos/"+id)
			w.WriteHeader(http.StatusCreated)
			return
		}
		n, _ := io.Copy(io.Discard, r.Body)
		w.Header().Set("upload-offset", strconv.FormatInt(n, 10))
		w.Header().Set("x-ftuna-repo", "/"+account+"/"+id+"/")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "aarch64", "app.rpm"), []byte{0xed, 0xab, 0xee, 0xdb})
	writeFile(t, filepath.Join(dir, "aarch64", "repodata", "repomd.xml"), []byte("<repomd/>"))
	repo, err := Publish(context.Background(), srv.URL, account+".secret", dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if repo != "/"+account+"/"+id+"/" {
		t.Fatalf("repo %q", repo)
	}
}

func TestRegistered(t *testing.T) {
	const account = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	token := account + ".secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "{\"account\":%q}\n", account)
	}))
	defer srv.Close()
	ok, err := Registered(context.Background(), srv.URL, token)
	if err != nil || !ok {
		t.Fatalf("registered %v %v", ok, err)
	}
	ok, err = Registered(context.Background(), srv.URL, account+".other")
	if err != nil || ok {
		t.Fatalf("revoked %v %v", ok, err)
	}
}

func TestCheckName(t *testing.T) {
	for _, name := range []string{"a", "harbour-advanced-camera", "pr_213"} {
		if err := CheckName(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"", "Nope", "has.dot", "has/slash", "-leading"} {
		if err := CheckName(name); err == nil {
			t.Errorf("want reject %s", name)
		}
	}
}

func TestPublishAsRejectsName(t *testing.T) {
	_, err := Publish(context.Background(), "http://127.0.0.1", "token", t.TempDir(), "Nope")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPublishLive(t *testing.T) {
	base := os.Getenv("FTUNA_SCRATCH_URL")
	if base == "" {
		t.Skip()
	}
	ctx := context.Background()
	_, token, err := Register(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rpm := append([]byte{0xed, 0xab, 0xee, 0xdb}, bytesOf(64)...)
	writeFile(t, filepath.Join(dir, "aarch64", "app.rpm"), rpm)
	writeFile(t, filepath.Join(dir, "aarch64", "repodata", "repomd.xml"), []byte("<repomd/>"))
	const name = "live"
	repo, err := Publish(ctx, base, token, dir, name)
	if err != nil {
		t.Fatal(err)
	}
	account, err := Account(token)
	if err != nil {
		t.Fatal(err)
	}
	if repo != "/"+account+"/"+name+"/" {
		t.Fatalf("repo %q", repo)
	}
}

func TestPublishReplace(t *testing.T) {
	base := os.Getenv("FTUNA_SCRATCH_URL")
	if base == "" {
		t.Skip()
	}
	ctx := context.Background()
	_, token, err := Register(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	const name = "github_mrcyjanek_sfos_mullvad_pr_213"
	first := t.TempDir()
	writeFile(t, filepath.Join(first, "aarch64", "old.rpm"), append([]byte{0xed, 0xab, 0xee, 0xdb}, 1))
	writeFile(t, filepath.Join(first, "aarch64", "repodata", "repomd.xml"), []byte("<old/>"))
	repo, err := Publish(ctx, base, token, first, name)
	if err != nil {
		t.Fatal(err)
	}
	if repo != "/"+mustAccount(t, token)+"/"+name+"/" {
		t.Fatalf("repo %q", repo)
	}
	second := t.TempDir()
	writeFile(t, filepath.Join(second, "aarch64", "new.rpm"), append([]byte{0xed, 0xab, 0xee, 0xdb}, 2))
	writeFile(t, filepath.Join(second, "aarch64", "repodata", "repomd.xml"), []byte("<new/>"))
	again, err := Publish(ctx, base, token, second, name)
	if err != nil {
		t.Fatal(err)
	}
	if again != repo {
		t.Fatalf("replaced repo %q, want %q", again, repo)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "scratchpad-repo.ftuna.eu" {
		return
	}
	t.Logf("repo %s%s", base, repo)
	if status := getStatus(t, base+repo+"aarch64/old.rpm"); status != http.StatusNotFound {
		t.Fatalf("old rpm status %d", status)
	}
	if status := getStatus(t, base+repo+"aarch64/new.rpm"); status != http.StatusOK {
		t.Fatalf("new rpm status %d", status)
	}
}

func TestUploadChunks(t *testing.T) {
	const account = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const id = "chunky"
	var patches int
	var got, length int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("tus-resumable", "1.0.0")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/repos":
			if r.Header.Get("x-ftuna-id") != id {
				t.Errorf("id %q", r.Header.Get("x-ftuna-id"))
			}
			length, _ = strconv.ParseInt(r.Header.Get("upload-length"), 10, 64)
			w.Header().Set("location", "/v1/repos/"+id)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch:
			patches++
			off, _ := strconv.ParseInt(r.Header.Get("upload-offset"), 10, 64)
			if off != got {
				http.Error(w, "offset", http.StatusConflict)
				return
			}
			n, _ := io.Copy(io.Discard, r.Body)
			got += n
			w.Header().Set("upload-offset", strconv.FormatInt(got, 10))
			if got == length {
				w.Header().Set("x-ftuna-repo", "/"+account+"/"+id+"/")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	// Larger than one 8 MiB chunk, stored uncompressed inside the tar via a sparse-looking payload.
	payload := bytesRepeat(chunkSize + 4096)
	writeFile(t, filepath.Join(dir, "aarch64", "big.rpm"), append([]byte{0xed, 0xab, 0xee, 0xdb}, payload...))
	writeFile(t, filepath.Join(dir, "aarch64", "repodata", "repomd.xml"), []byte("<repomd/>"))
	repo, err := Publish(context.Background(), srv.URL, account+".secret", dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if repo != "/"+account+"/"+id+"/" {
		t.Fatalf("repo %q", repo)
	}
	if patches < 2 {
		t.Fatalf("patches %d, want at least 2", patches)
	}
}

func TestSolveFindsLeadingZeros(t *testing.T) {
	sol, err := Solve(context.Background(), "abc", 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("abc" + sol))
	if zeroBits(sum[:]) < 12 {
		t.Fatalf("solution %s has %d leading zero bits", sol, zeroBits(sum[:]))
	}
}

func TestSolveStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Solve(ctx, "abc", 40, nil); err == nil {
		t.Fatal("expected cancel")
	}
}

func TestTokenPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := tokenPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "ftuna", "scratch-token")
	if got != want {
		t.Fatalf("token path %s, want %s", got, want)
	}
}

func mustAccount(t *testing.T, token string) string {
	t.Helper()
	account, err := Account(token)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func getStatus(t *testing.T, raw string) int {
	t.Helper()
	res, err := http.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}

func bytesOf(n int) []byte {
	return bytesRepeat(n)
}

func bytesRepeat(n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(i)
	}
	return buf
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
