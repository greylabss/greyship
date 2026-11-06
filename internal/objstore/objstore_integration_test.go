//go:build integration

package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"greyship.tech/internal/objstore"
)

// testStore returns a store on the greyship-test bucket that make infra creates in the dev Garage.
func testStore(t *testing.T) *objstore.Store {
	t.Helper()
	endpoint := os.Getenv("GREYSHIP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("GREYSHIP_TEST_S3_ENDPOINT not set; run make test-int")
	}
	return &objstore.Store{
		Endpoint: endpoint, Region: os.Getenv("GREYSHIP_TEST_S3_REGION"), Bucket: "greyship-test",
		AccessKey: os.Getenv("GREYSHIP_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("GREYSHIP_TEST_S3_SECRET_KEY"),
		HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
	}
}

func put(t *testing.T, url string, body []byte, length int64) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = length
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// A server may close the connection on a body that doesn't match the signed length.
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestPresignedRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	key := "it/" + time.Now().Format("150405.000000") + ".tar.gz"
	body := []byte("pretend tarball")

	if _, err := s.Stat(ctx, key); !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("Stat before upload = %v, want ErrNotFound", err)
	}

	tooBig, err := s.PresignPut(key, 4, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code := put(t, tooBig, body, int64(len(body))); code == http.StatusOK {
		t.Fatal("storage accepted a body larger than the signed content-length")
	}

	url, err := s.PresignPut(key, int64(len(body)), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code := put(t, url, body, int64(len(body))); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	size, err := s.Stat(ctx, key)
	if err != nil || size != int64(len(body)) {
		t.Fatalf("Stat = %d, %v", size, err)
	}

	get, err := s.PresignGet(key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(get)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("GET = %d %q", resp.StatusCode, got)
	}

	wrong := *s
	wrong.SecretKey = "not-the-secret"
	forged, _ := wrong.PresignPut(key, int64(len(body)), time.Minute)
	if code := put(t, forged, body, int64(len(body))); code != http.StatusForbidden {
		t.Fatalf("PUT with a bad signature = %d, want 403", code)
	}
}
