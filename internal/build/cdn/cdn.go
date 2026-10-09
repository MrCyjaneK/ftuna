// Package cdn copies upstream icon and screenshot objects onto cdn-mirror.ftuna.eu.
package cdn

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

const (
	OriginHost    = "raw.githubusercontent.com"
	OpenReposHost = "openrepos.net"
	openReposRoot = "/sites/default/files/"
	MirrorHost    = "cdn-mirror.ftuna.eu"
	MirrorPrefix  = "cdn-mirror"
)

var client = &http.Client{Timeout: 60 * time.Second}

// Asset is one mirrored object under cdn-mirror.ftuna.eu.
type Asset struct {
	Host string
	Path string
}

// ParseAsset checks a raw GitHub or OpenRepos file URL.
func ParseAsset(raw string) (Asset, error) {
	if p, err := originPath(OriginHost, raw, ""); err == nil {
		return Asset{OriginHost, p}, nil
	}
	if p, err := originPath(OpenReposHost, raw, openReposRoot); err == nil {
		return Asset{OpenReposHost, p}, nil
	}
	return Asset{}, fmt.Errorf("%q must be https://%s/... or https://%s%s...", raw, OriginHost, OpenReposHost, openReposRoot)
}

// OriginPath checks a raw GitHub URL and returns its absolute path.
func OriginPath(raw string) (string, error) {
	a, err := ParseAsset(raw)
	if err != nil {
		return "", err
	}
	if a.Host != OriginHost {
		return "", fmt.Errorf("%q must be https://%s/...", raw, OriginHost)
	}
	return a.Path, nil
}

func (a Asset) ObjectKey() string {
	return MirrorPrefix + "/" + a.Host + a.Path
}

func (a Asset) PublicURL() string {
	switch a.Host {
	case OpenReposHost:
		parts := strings.Split(strings.TrimPrefix(a.Path, "/"), "/")
		esc := make([]string, len(parts))
		for i, part := range parts {
			esc[i] = url.PathEscape(part)
		}
		return "https://" + MirrorHost + "/" + OpenReposHost + "/" + strings.Join(esc, "/")
	default:
		return "https://" + MirrorHost + "/" + a.Host + a.Path
	}
}

func ObjectKey(originPath string) string {
	return Asset{OriginHost, originPath}.ObjectKey()
}

func PublicURL(originPath string) string {
	return Asset{OriginHost, originPath}.PublicURL()
}

// Ensure stores the origin object when it is missing and returns the CDN URL.
func Ensure(ctx context.Context, store *s3store.Store, raw string) (string, error) {
	a, err := ParseAsset(raw)
	if err != nil {
		return "", err
	}
	key := a.ObjectKey()
	public := a.PublicURL()
	if _, ok, err := store.Head(ctx, key); err != nil {
		return "", err
	} else if ok {
		return public, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ftuna")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("GET %s: %s", raw, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	if _, err := store.PutBytes(ctx, key, contentType(a.Path), body, nil); err != nil {
		return "", err
	}
	return public, nil
}

func originPath(host, raw, pathRoot string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	if u.Scheme != "https" || u.Host != host {
		return "", fmt.Errorf("wrong host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q has extra URL parts", raw)
	}
	p := u.Path
	if pathRoot != "" {
		if !strings.HasPrefix(p, pathRoot) {
			return "", fmt.Errorf("wrong path")
		}
	} else if p == "" || p == "/" || !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%q has an invalid path", raw)
	}
	if strings.Contains(p, "\\") || strings.Contains(p, "\x00") {
		return "", fmt.Errorf("%q has an invalid path", raw)
	}
	if path.Clean(p) != p || strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") {
		return "", fmt.Errorf("%q is not a clean path", raw)
	}
	return p, nil
}

func contentType(originPath string) string {
	switch strings.ToLower(path.Ext(originPath)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}
