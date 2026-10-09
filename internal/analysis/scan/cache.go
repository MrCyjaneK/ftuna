package scan

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/extract"
)

// rpmCacheDir is ~/.cache/ftuna/rpmcache, or $XDG_CACHE_HOME/ftuna/rpmcache
// when that variable is set. override wins. Nothing in this package deletes
// a cached RPM.
func rpmCacheDir(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return override, nil
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "ftuna", "rpmcache"), nil
}

func cachePath(dir, sha string) (string, error) {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !validSHA256(sha) {
		return "", fmt.Errorf("bad sha256 %q", sha)
	}
	return filepath.Join(dir, sha), nil
}

func validSHA256(sha string) bool {
	if len(sha) != sha256.Size*2 {
		return false
	}
	for _, c := range sha {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			continue
		default:
			return false
		}
	}
	return true
}

func readCachedRPM(dir, sha string) ([]byte, bool) {
	if dir == "" {
		return nil, false
	}
	path, err := cachePath(dir, sha)
	if err != nil {
		return nil, false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	if err := extract.Verify("sha256", sha, body); err != nil {
		log.Printf("ignoring rpm cache %s", sha)
		return nil, false
	}
	return body, true
}

func writeCachedRPM(dir, sha string, body []byte) error {
	if dir == "" {
		return nil
	}
	path, err := cachePath(dir, sha)
	if err != nil {
		return err
	}
	if current, ok := readCachedRPM(dir, sha); ok && bytes.Equal(current, body) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rpm-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}
