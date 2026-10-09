package openrepos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

const userAgent = "FTunaMirror/0.1"

func httpClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			// Auto-decompression would store different bytes than upstream.
			DisableCompression: true,
		},
	}
}

func get(ctx context.Context, client *http.Client, url string, extra http.Header) (*http.Response, error) {
	var last error
	for attempt := 1; attempt <= 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept-Encoding", "identity")
		for key, values := range extra {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			last = err
		} else if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			last = fmt.Errorf("%s: %s", url, resp.Status)
			resp.Body.Close()
		} else {
			return resp, nil
		}
		if attempt < 4 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}
	return nil, last
}

func download(ctx context.Context, client *http.Client, url string) (string, *s3store.HashWriter, error) {
	resp, err := get(ctx, client, url, nil)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp("", "ftuna-mirror-*")
	if err != nil {
		return "", nil, err
	}
	hw := s3store.NewHashWriter()
	if _, err := io.Copy(f, io.TeeReader(resp.Body, hw)); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), hw, nil
}

func downloadBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	got, err := fetch(ctx, client, url, "")
	if err != nil {
		return nil, err
	}
	if got.Status != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d", url, got.Status)
	}
	return got.Body, nil
}

type fetched struct {
	Status int
	Body   []byte
	ETag   string
}

// fetch performs one GET. ifNoneMatch is sent as If-None-Match when set.
// A 304 response has an empty body.
func fetch(ctx context.Context, client *http.Client, rawURL, ifNoneMatch string) (fetched, error) {
	var extra http.Header
	if ifNoneMatch != "" {
		extra = http.Header{"If-None-Match": []string{ifNoneMatch}}
	}
	resp, err := get(ctx, client, rawURL, extra)
	if err != nil {
		return fetched{}, err
	}
	defer resp.Body.Close()
	out := fetched{Status: resp.StatusCode, ETag: resp.Header.Get("ETag")}
	if resp.StatusCode == http.StatusNotModified || resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return out, nil
	}
	if resp.StatusCode != http.StatusOK {
		return fetched{}, fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	out.Body, err = io.ReadAll(resp.Body)
	return out, err
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
