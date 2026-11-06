package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T, api string) (*App, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &App{ID: 42, Slug: "greyship", ClientID: "Iv1.x", ClientSecret: "s", Key: key, API: api, Web: api, HTTP: http.DefaultClient, Now: time.Now}, key
}

func TestParseKey(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if k, err := ParseKey(pkcs1); err != nil || k.N.Cmp(key.N) != 0 {
		t.Fatalf("pkcs1: %v", err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if _, err := ParseKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		t.Fatalf("pkcs8: %v", err)
	}
	if _, err := ParseKey([]byte("nope")); err == nil {
		t.Fatal("accepted a non-PEM key")
	}
}

// The app token must be an RS256 JWT GitHub can verify with the app's public key.
func TestInstallationTokenUsesSignedJWT(t *testing.T) {
	var app *App
	var key *rsa.PrivateKey
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		jwt, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(jwt, ".")
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if r.URL.Path != "/app/installations/7/access_tokens" || rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		if !strings.Contains(string(claims), `"iss":"42"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": time.Now().Add(time.Hour)})
	}))
	defer srv.Close()
	app, key = testApp(t, srv.URL)
	for range 2 {
		tok, err := app.InstallationToken(context.Background(), 7)
		if err != nil || tok != "ghs_x" {
			t.Fatalf("token %q %v", tok, err)
		}
	}
	if calls != 1 {
		t.Fatalf("token fetched %d times, want once and then cached", calls)
	}
}

func TestValidSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	tests := []struct {
		name, secret, header string
		body                 []byte
		ok                   bool
	}{
		{"valid", "secret", good, body, true},
		{"other secret", "other", good, body, false},
		{"tampered body", "secret", good, []byte(`{"ref":"refs/heads/evil"}`), false},
		{"no prefix", "secret", strings.TrimPrefix(good, "sha256="), body, false},
		{"empty secret never matches", "", good, body, false},
	}
	for _, tt := range tests {
		if got := ValidSignature(tt.secret, tt.body, tt.header); got != tt.ok {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.ok)
		}
	}
}

func TestValidRepo(t *testing.T) {
	for s, ok := range map[string]bool{"haki/temp": true, "Org.Name/repo-1_x": true, "../etc": false, "a/b/c": false, "a/..": false, "a": false} {
		if ValidRepo(s) != ok {
			t.Errorf("ValidRepo(%q) = %v", s, !ok)
		}
	}
}

func githubArchive(t *testing.T, files map[string]string, links map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader})
	_ = tw.WriteHeader(&tar.Header{Name: "haki-temp-abc123/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: "haki-temp-abc123/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	for name, target := range links {
		_ = tw.WriteHeader(&tar.Header{Name: "haki-temp-abc123/" + name, Typeflag: tar.TypeSymlink, Linkname: target})
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func names(t *testing.T, b []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
	sort.Strings(out)
	return out
}

func TestRepack(t *testing.T) {
	archive := githubArchive(t, map[string]string{
		"go.mod":          "module x\n",
		"cmd/api/main.go": "package main\n",
		".env":            "SECRET=1",
		".env.prod":       "SECRET=2",
		".gitignore":      "tmp/\n",
		"tmp/big.bin":     "junk",
	}, map[string]string{"link": "/etc/passwd"})
	var out bytes.Buffer
	if _, err := Repack(archive, &out); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(t, out.Bytes()), ",")
	if got != ".gitignore,cmd/api/main.go,go.mod" {
		t.Fatalf("repacked %s: .env files, ignored files and symlinks must never be in a deploy", got)
	}

	for name, bad := range map[string][]byte{
		"escape":  githubArchive(t, map[string]string{"../../evil": "x"}, nil),
		"garbage": []byte("not an archive"),
	} {
		if _, err := Repack(bad, io.Discard); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
