package objstore

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The presigned GET example from AWS's "Authenticating Requests: Using Query Parameters" documentation.
func TestPresignAWSExample(t *testing.T) {
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt")
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	got := presign("GET", u, nil, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", now, 24*time.Hour)
	want := "X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("presign = %s\nwant suffix %s", got, want)
	}
	if !strings.Contains(got, "X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request") {
		t.Fatalf("credential not encoded as expected: %s", got)
	}
}

func TestPresignPutSignsContentLength(t *testing.T) {
	s := &Store{Endpoint: "http://localhost:9000/", Region: "us-east-1", Bucket: "greyship", AccessKey: "k", SecretKey: "s", Now: func() time.Time { return time.Unix(0, 0) }}
	got, err := s.PresignPut("sources/p/d.tar.gz", 1234, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/greyship/sources/p/d.tar.gz" {
		t.Errorf("path = %s", u.Path)
	}
	if h := u.Query().Get("X-Amz-SignedHeaders"); h != "content-length;host" {
		t.Errorf("signed headers = %s", h)
	}
	if e := u.Query().Get("X-Amz-Expires"); e != "900" {
		t.Errorf("expires = %s", e)
	}
}

func TestPresignRejectsBadEndpoint(t *testing.T) {
	s := &Store{Endpoint: "localhost", Now: time.Now}
	if _, err := s.PresignGet("k", time.Minute); err == nil {
		t.Fatal("want error for endpoint without scheme")
	}
}

func TestEncode(t *testing.T) {
	tests := []struct {
		in    string
		slash bool
		want  string
	}{
		{"a-b_c.d~e", true, "a-b_c.d~e"},
		{"/bucket/a b/c+d", false, "/bucket/a%20b/c%2Bd"},
		{"a/b", true, "a%2Fb"},
		{"ü", true, "%C3%BC"},
	}
	for _, tt := range tests {
		if got := encode(tt.in, tt.slash); got != tt.want {
			t.Errorf("encode(%q, %v) = %q, want %q", tt.in, tt.slash, got, tt.want)
		}
	}
}
