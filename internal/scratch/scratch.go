// Package scratch publishes a one-off repo to scratchpad-repo.ftuna.eu.
package scratch

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	scratchID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	repoName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)
)

// 90 MiB stays under the 100 MB request-body cap on a Free Cloudflare zone.
const chunkSize = 90 << 20

func BaseURL() string {
	if v := os.Getenv("FTUNA_SCRATCH_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://scratchpad-repo.ftuna.eu"
}

func Register(ctx context.Context, base string) (account, token string, err error) {
	challenge, bits, err := Challenge(ctx, base)
	if err != nil {
		return "", "", err
	}
	solution, err := Solve(ctx, challenge, bits, nil)
	if err != nil {
		return "", "", err
	}
	return Submit(ctx, base, challenge, solution)
}

func Challenge(ctx context.Context, base string) (challenge string, bits int, err error) {
	res, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/challenge", nil)
	if err != nil {
		return "", 0, err
	}
	body, err := do(res)
	if err != nil {
		return "", 0, err
	}
	var ch struct {
		Challenge string `json:"challenge"`
		Bits      int    `json:"bits"`
	}
	if err := json.Unmarshal(body, &ch); err != nil {
		return "", 0, err
	}
	if ch.Challenge == "" || ch.Bits < 1 {
		return "", 0, fmt.Errorf("challenge: missing fields")
	}
	return ch.Challenge, ch.Bits, nil
}

func Submit(ctx context.Context, base, challenge, solution string) (account, token string, err error) {
	payload, _ := json.Marshal(map[string]string{
		"challenge": challenge,
		"solution":  solution,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/register", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("content-type", "application/json")
	body, err := do(req)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Account string `json:"account"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", err
	}
	if out.Token == "" {
		return "", "", fmt.Errorf("register: empty token")
	}
	return out.Account, out.Token, nil
}

// Registered reports whether token still names an account on base.
// A missing account returns false and a nil error.
func Registered(ctx context.Context, base, token string) (bool, error) {
	account, err := Account(token)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/account", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return false, err
	}
	switch res.StatusCode {
	case http.StatusOK:
		var out struct {
			Account string `json:"account"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return false, err
		}
		if out.Account != account {
			return false, fmt.Errorf("account: got %s, want %s", out.Account, account)
		}
		return true, nil
	case http.StatusUnauthorized:
		return false, nil
	default:
		return false, fmt.Errorf("account: %s: %s", res.Status, bytes.TrimSpace(body))
	}
}

// Account is the id stored in a token, which has the form account.secret.
func Account(token string) (string, error) {
	account, _, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || !scratchID.MatchString(account) {
		return "", fmt.Errorf("scratch token: bad account")
	}
	return account, nil
}

// CheckName reports whether id can be a scratch directory. It is 1-80
// characters from a-z, 0-9, underscore, and hyphen, and it starts with a
// letter or digit.
func CheckName(id string) error {
	if !repoName.MatchString(id) {
		return fmt.Errorf("scratch name: use 1-80 characters of a-z, 0-9, _, or -")
	}
	return nil
}

// Publish packs dir as an uncompressed ustar archive and uploads it as id.
// A directory of RPM files is arranged into aarch64, armv7hl, and i486
// repositories and given repodata first. A tree that already has
// repodata/repomd.xml for each architecture is uploaded unchanged.
// Publishing the same name again replaces that repo.
// It returns the public path, /<account>/<id>/.
func Publish(ctx context.Context, base, token, dir, id string) (string, error) {
	if err := CheckName(id); err != nil {
		return "", err
	}
	return publish(ctx, base, token, dir, id)
}

func publish(ctx context.Context, base, token, dir, id string) (string, error) {
	log.Printf("packing %s as %s", dir, id)
	f, err := os.CreateTemp("", "ftuna-scratch-*.tar")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	staged, cleanup, err := stageRepo(dir)
	if err != nil {
		return "", err
	}
	defer cleanup()
	if err := writeTar(f, staged); err != nil {
		return "", err
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	log.Printf("archive %s", byteSize(size))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/repos", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("tus-resumable", "1.0.0")
	req.Header.Set("upload-length", strconv.FormatInt(size, 10))
	req.Header.Set("x-ftuna-id", id)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		if s := bytes.TrimSpace(msg); len(s) > 0 {
			return "", fmt.Errorf("create upload: %s: %s", res.Status, s)
		}
		return "", fmt.Errorf("create upload: %s", res.Status)
	}
	loc, err := resolve(base+"/v1/repos", res.Header.Get("location"))
	if err != nil {
		return "", err
	}
	buf := make([]byte, chunkSize)
	var repo string
	stalls := 0
	for {
		at, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return "", err
		}
		var body []byte
		if at < size {
			n, err := f.Read(buf)
			if n == 0 && err == io.EOF {
				break
			}
			if err != nil && err != io.EOF {
				return "", err
			}
			body = buf[:n]
		} else if repo != "" {
			break
		}
		next, got, err := sendChunk(ctx, loc, token, at, body)
		if err != nil {
			return "", err
		}
		if got != "" {
			repo = got
		}
		if next < at {
			return "", fmt.Errorf("upload: offset went backwards")
		}
		if _, err := f.Seek(next, io.SeekStart); err != nil {
			return "", err
		}
		if got == "" && next == at {
			stalls++
			if stalls > 10000 {
				return "", fmt.Errorf("upload stalled")
			}
			log.Printf("upload finishing %s/%s", byteSize(size), byteSize(size))
			continue
		}
		stalls = 0
		if next > at {
			log.Printf("upload %s/%s", byteSize(next), byteSize(size))
		}
	}
	if repo == "" {
		return "", fmt.Errorf("upload finished without a repo url")
	}
	account, err := Account(token)
	if err != nil {
		return "", err
	}
	want := "/" + account + "/" + id + "/"
	if repo != want {
		return "", fmt.Errorf("scratch repo %s, want %s", repo, want)
	}
	return repo, nil
}

func byteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

const chunkAttempts = 6

func sendChunk(ctx context.Context, loc, token string, at int64, body []byte) (next int64, repo string, err error) {
	var pause time.Duration
	var last error
	for attempt := 1; attempt <= chunkAttempts; attempt++ {
		if pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return 0, "", ctx.Err()
			case <-timer.C:
			}
		}
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		next, repo, retry, err := patchChunk(reqCtx, loc, token, at, body)
		cancel()
		if !retry {
			return next, repo, err
		}
		last = err
		if off, got, herr := headProgress(ctx, loc, token); herr == nil && off > at {
			return off, got, nil
		}
		if attempt == chunkAttempts {
			break
		}
		pause = time.Second << (attempt - 1)
		if pause > 8*time.Second {
			pause = 8 * time.Second
		}
		log.Printf("upload retry %s: %v", byteSize(at), err)
	}
	if last == nil {
		last = fmt.Errorf("upload: storage error")
	}
	return 0, "", last
}

func patchChunk(ctx context.Context, loc, token string, at int64, body []byte) (next int64, repo string, retry bool, err error) {
	part, err := http.NewRequestWithContext(ctx, http.MethodPatch, loc, bytes.NewReader(body))
	if err != nil {
		return 0, "", false, err
	}
	part.Header.Set("authorization", "Bearer "+token)
	part.Header.Set("tus-resumable", "1.0.0")
	part.Header.Set("content-type", "application/offset+octet-stream")
	part.Header.Set("upload-offset", strconv.FormatInt(at, 10))
	part.ContentLength = int64(len(body))
	res, err := http.DefaultClient.Do(part)
	if err != nil {
		return 0, "", true, err
	}
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		off, got, err := headProgress(ctx, loc, token)
		if err != nil {
			return 0, "", false, err
		}
		log.Printf("resume at %s", byteSize(off))
		return off, got, false, nil
	}
	if retryable(res.StatusCode) {
		return 0, "", true, fmt.Errorf("upload: %s: %s", res.Status, bytes.TrimSpace(msg))
	}
	if res.StatusCode != http.StatusNoContent {
		return 0, "", false, fmt.Errorf("upload: %s: %s", res.Status, bytes.TrimSpace(msg))
	}
	next, err = strconv.ParseInt(res.Header.Get("upload-offset"), 10, 64)
	if err != nil {
		return 0, "", false, fmt.Errorf("upload: bad offset")
	}
	return next, res.Header.Get("x-ftuna-repo"), false, nil
}

func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// Solve searches for a decimal suffix whose SHA-256 has bits leading zero bits.
// Workers share the search. The first match is returned.
func Solve(ctx context.Context, challenge string, bits int, hashes *atomic.Uint64) (string, error) {
	if bits < 1 {
		return "", fmt.Errorf("proof of work: bad bits %d", bits)
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu  sync.Mutex
		sol string
		hit bool
	)
	var wg sync.WaitGroup
	prefix := []byte(challenge)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(start uint64) {
			defer wg.Done()
			buf := make([]byte, 0, len(prefix)+20)
			buf = append(buf, prefix...)
			base := len(buf)
			stride := uint64(workers)
			var n uint64
			if hashes != nil {
				defer func() { hashes.Add(n) }()
			}
			for i := start; ; i += stride {
				buf = strconv.AppendUint(buf[:base], i, 10)
				sum := sha256.Sum256(buf)
				n++
				if zeroBits(sum[:]) >= bits {
					mu.Lock()
					if !hit {
						sol = string(buf[base:])
						hit = true
						cancel()
					}
					mu.Unlock()
					return
				}
				if i&0x3ff == start&0x3ff {
					if hashes != nil {
						hashes.Add(n)
						n = 0
					}
					select {
					case <-ctx.Done():
						return
					default:
					}
				}
			}
		}(uint64(w))
	}
	wg.Wait()
	if hit {
		return sol, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("proof of work stopped")
}

func SaveToken(token string) (string, error) {
	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func LoadToken() (string, error) {
	if v := os.Getenv("FTUNA_SCRATCH_TOKEN"); v != "" {
		return v, nil
	}
	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("scratch token: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func tokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ftuna", "scratch-token"), nil
}

func writeTar(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", rel)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.AccessTime = time.Time{}
		hdr.ChangeTime = time.Time{}
		hdr.Format = tar.FormatUSTAR
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, in)
		in.Close()
		return err
	})
	if err != nil {
		tw.Close()
		return err
	}
	return tw.Close()
}

func resolve(base, loc string) (string, error) {
	if loc == "" {
		return "", fmt.Errorf("create upload: missing location")
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(loc)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(u).String(), nil
}

func headProgress(ctx context.Context, loc, token string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, loc, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("tus-resumable", "1.0.0")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("resume: %s", res.Status)
	}
	off, err := strconv.ParseInt(res.Header.Get("upload-offset"), 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("resume: bad offset")
	}
	return off, res.Header.Get("x-ftuna-repo"), nil
}

func do(req *http.Request) ([]byte, error) {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s: %s", res.Status, bytes.TrimSpace(body))
	}
	return body, nil
}

func zeroBits(sum []byte) int {
	n := 0
	for _, b := range sum {
		if b == 0 {
			n += 8
			continue
		}
		for mask := byte(0x80); mask > 0 && b&mask == 0; mask >>= 1 {
			n++
		}
		break
	}
	return n
}
