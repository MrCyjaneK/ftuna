// Package s3store is the FTuna object store. Durable state lives here.
// Callers keep only the file they are currently hashing on local disk.
package s3store

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	MetaSHA256 = "sha256"
	MetaSHA1   = "sha1"
)

type Store struct {
	client *s3.Client
	bucket string
}

type ObjectHead struct {
	Size   int64
	SHA256 string
	SHA1   string
	ETag   string
}

type PutResult struct {
	Size   int64
	SHA256 string
	SHA1   string
	MD5    string
	ETag   string
}

func FromEnv() (*Store, error) {
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET_NAME"))
	region := strings.TrimSpace(os.Getenv("S3_REGION"))
	key := os.Getenv("S3_ACCESS_KEY_ID")
	secret := os.Getenv("S3_SECRET_ACCESS_KEY")
	missing := []string{}
	for _, pair := range [][2]string{
		{"S3_ENDPOINT", endpoint},
		{"S3_BUCKET_NAME", bucket},
		{"S3_REGION", region},
		{"S3_ACCESS_KEY_ID", key},
		{"S3_SECRET_ACCESS_KEY", secret},
	} {
		if pair[1] == "" {
			missing = append(missing, pair[0])
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	cfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(key, secret, ""),
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
		// Backblaze rejects the default CRC32 checksum headers.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &Store{client: client, bucket: bucket}, nil
}

func (s *Store) Bucket() string { return s.bucket }

func (s *Store) Head(ctx context.Context, key string) (ObjectHead, bool, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return ObjectHead{}, false, nil
		}
		return ObjectHead{}, false, err
	}
	h := ObjectHead{ETag: strings.Trim(aws.ToString(out.ETag), `"`)}
	if out.ContentLength != nil {
		h.Size = *out.ContentLength
	}
	if out.Metadata != nil {
		h.SHA256 = out.Metadata[MetaSHA256]
		h.SHA1 = out.Metadata[MetaSHA1]
	}
	return h, true, nil
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	body, ok, err := s.GetIfExists(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("s3 object %s not found", key)
	}
	return body, nil
}

func (s *Store) GetFile(ctx context.Context, key, path string) error {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("s3 object %s not found", key)
		}
		return err
	}
	defer out.Body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, out.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (s *Store) GetIfExists(ctx context.Context, key string) ([]byte, bool, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

func (s *Store) PutBytes(ctx context.Context, key, contentType string, body []byte, meta map[string]string) (PutResult, error) {
	sums := hashBytes(body)
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String(contentType),
		Metadata:      mergeMeta(meta, sums),
	}
	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		return PutResult{}, err
	}
	res := PutResult{Size: int64(len(body)), SHA256: sums.sha256, SHA1: sums.sha1, MD5: sums.md5, ETag: strings.Trim(aws.ToString(out.ETag), `"`)}
	if err := checkETag(res.ETag, res.MD5); err != nil {
		return res, fmt.Errorf("put %s: %w", key, err)
	}
	return res, nil
}

func (s *Store) PutFile(ctx context.Context, key, contentType, path string, meta map[string]string) (PutResult, error) {
	sums, size, err := hashFile(path)
	if err != nil {
		return PutResult{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return PutResult{}, err
	}
	defer f.Close()
	out, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
		Metadata:      mergeMeta(meta, sums),
	})
	if err != nil {
		return PutResult{}, err
	}
	res := PutResult{Size: size, SHA256: sums.sha256, SHA1: sums.sha1, MD5: sums.md5, ETag: strings.Trim(aws.ToString(out.ETag), `"`)}
	if err := checkETag(res.ETag, res.MD5); err != nil {
		return res, fmt.Errorf("put %s: %w", key, err)
	}
	return res, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// List returns every key under prefix. A truncated listing is an error.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	return s.ListNotify(ctx, prefix, nil)
}

// ListNotify lists every key under prefix. progress, when set, is called
// before each page with the number of keys collected so far.
func (s *Store) ListNotify(ctx context.Context, prefix string, progress func(keys int)) ([]string, error) {
	var keys []string
	var token *string
	for {
		if progress != nil {
			progress(len(keys))
		}
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if aws.ToBool(out.IsTruncated) {
			if out.NextContinuationToken == nil || *out.NextContinuationToken == "" {
				return nil, fmt.Errorf("list %s: truncated response without a continuation token", prefix)
			}
			token = out.NextContinuationToken
			continue
		}
		return keys, nil
	}
}

type digests struct {
	sha256 string
	sha1   string
	md5    string
}

func mergeMeta(extra map[string]string, d digests) map[string]string {
	out := map[string]string{
		MetaSHA256: d.sha256,
		MetaSHA1:   d.sha1,
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func checkETag(etag, md5hex string) error {
	if etag == "" {
		return fmt.Errorf("empty etag")
	}
	if strings.Contains(etag, "-") {
		return fmt.Errorf("multipart etag %s", etag)
	}
	if !strings.EqualFold(etag, md5hex) {
		return fmt.Errorf("etag %s does not match md5 %s", etag, md5hex)
	}
	return nil
}

func hashBytes(b []byte) digests {
	s256 := sha256.Sum256(b)
	s1 := sha1.Sum(b)
	m := md5.Sum(b)
	return digests{sha256: hex.EncodeToString(s256[:]), sha1: hex.EncodeToString(s1[:]), md5: hex.EncodeToString(m[:])}
}

func hashFile(path string) (digests, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return digests{}, 0, err
	}
	defer f.Close()
	h256 := sha256.New()
	h1 := sha1.New()
	hm := md5.New()
	n, err := io.Copy(io.MultiWriter(h256, h1, hm), f)
	if err != nil {
		return digests{}, 0, err
	}
	return digests{
		sha256: hex.EncodeToString(h256.Sum(nil)),
		sha1:   hex.EncodeToString(h1.Sum(nil)),
		md5:    hex.EncodeToString(hm.Sum(nil)),
	}, n, nil
}

// HashWriter counts hashes of a stream. sha1 is required because OpenRepos
// repodata still labels package checksums as type "sha".
type HashWriter struct {
	sha256 hash.Hash
	sha1   hash.Hash
	md5    hash.Hash
	n      int64
}

func NewHashWriter() *HashWriter {
	return &HashWriter{sha256: sha256.New(), sha1: sha1.New(), md5: md5.New()}
}

func (h *HashWriter) Write(p []byte) (int, error) {
	n, err := io.MultiWriter(h.sha256, h.sha1, h.md5).Write(p)
	h.n += int64(n)
	return n, err
}

func (h *HashWriter) Size() int64 { return h.n }

func (h *HashWriter) SHA256() string { return hex.EncodeToString(h.sha256.Sum(nil)) }
func (h *HashWriter) SHA1() string   { return hex.EncodeToString(h.sha1.Sum(nil)) }
func (h *HashWriter) MD5() string    { return hex.EncodeToString(h.md5.Sum(nil)) }

func isNotFound(err error) bool {
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return true
		}
	}
	return false
}
