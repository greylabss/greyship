// Package objstore talks to S3-compatible object storage (Garage in development) with presigned URLs.
// It implements AWS Signature Version 4 query signing with the standard library (decisions.md 029).
package objstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("object not found")

type Store struct {
	Endpoint  string // scheme://host[:port], e.g. http://localhost:9000
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	HTTP      *http.Client
	Now       func() time.Time
}

// PresignPut returns a URL that accepts exactly one PUT of size bytes to key until it expires.
// Content-Length is signed, so a larger or smaller body is rejected by storage.
func (s *Store) PresignPut(key string, size int64, ttl time.Duration) (string, error) {
	return s.presign(http.MethodPut, key, map[string]string{"content-length": strconv.FormatInt(size, 10)}, ttl)
}

// PresignGet returns a URL that downloads key until it expires.
func (s *Store) PresignGet(key string, ttl time.Duration) (string, error) {
	return s.presign(http.MethodGet, key, nil, ttl)
}

// Stat returns the size of key, or ErrNotFound.
func (s *Store) Stat(ctx context.Context, key string) (int64, error) {
	u, err := s.presign(http.MethodHead, key, nil, time.Minute)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", key, err)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", key, err)
	}
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return 0, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return 0, fmt.Errorf("stat %s: storage answered %d", key, resp.StatusCode)
	}
	return resp.ContentLength, nil
}

func (s *Store) presign(method, key string, headers map[string]string, ttl time.Duration) (string, error) {
	u, err := url.Parse(strings.TrimRight(s.Endpoint, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid storage endpoint %q", s.Endpoint)
	}
	// Path-style addressing (/<bucket>/<key>), which Garage and MinIO need and S3 still accepts.
	u.Path = "/" + s.Bucket + "/" + strings.TrimLeft(key, "/")
	return presign(method, u, headers, s.AccessKey, s.SecretKey, s.Region, s.Now().UTC(), ttl), nil
}

// presign signs u with SigV4 query parameters. headers are extra signed headers (lowercase names); host is always signed.
func presign(method string, u *url.URL, headers map[string]string, accessKey, secretKey, region string, now time.Time, ttl time.Duration) string {
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	scope := day + "/" + region + "/s3/aws4_request"

	signed := map[string]string{"host": u.Host}
	for k, v := range headers {
		signed[k] = strings.TrimSpace(v)
	}
	names := make([]string, 0, len(signed))
	for k := range signed {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k + ":" + signed[k] + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	q := u.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	query := canonicalQuery(q)

	canonicalRequest := strings.Join([]string{method, encode(u.Path, false), query, canonicalHeaders.String(), signedHeaders, "UNSIGNED-PAYLOAD"}, "\n")
	digest := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(digest[:])

	key := hmacSHA256([]byte("AWS4"+secretKey), day)
	for _, part := range []string{region, "s3", "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	out := *u
	out.RawQuery = query + "&X-Amz-Signature=" + signature
	out.RawPath = encode(u.Path, false)
	return out.String()
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, encode(k, true)+"="+encode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// encode is SigV4's URI encoding: unreserved characters stay, everything else is %XX in upper case.
// Slashes stay in paths and are encoded in query values.
func encode(s string, slash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !slash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// Put uploads size bytes from body to key through a presigned URL.
func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	u, err := s.PresignPut(key, size, 15*time.Minute)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	req.ContentLength = size
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("put %s: storage answered %d", key, resp.StatusCode)
	}
	return nil
}

// Get downloads key, refusing objects larger than limit bytes.
func (s *Store) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	u, err := s.PresignGet(key, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("get %s: storage answered %d", key, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("get %s: object is larger than %d bytes", key, limit)
	}
	return b, nil
}
