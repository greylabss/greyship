// Package webpush sends browser push notifications with the Web Push standard: the message is encrypted for the
// browser (RFC 8291, aes128gcm) and the request is signed with Greyship's own key (VAPID, RFC 8292). Everything
// it needs is in the standard library.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Subscription is what a browser hands over when someone turns notifications on.
type Subscription struct {
	Endpoint string
	P256dh   string // the browser's public key, base64url
	Auth     string // the browser's auth secret, base64url
}

// ErrGone means the browser unsubscribed (or the subscription expired); forget it.
var ErrGone = errors.New("push subscription is gone")

// Sender signs and sends pushes.
type Sender struct {
	key     *ecdsa.PrivateKey
	public  []byte // uncompressed P-256 point, as browsers expect it
	subject string // a mailto: or https: contact for the push services
	client  *http.Client
	now     func() time.Time
}

// NewSender takes the VAPID private key as base64url of its 32-byte scalar (see GenerateKey).
func NewSender(privateKey, subject string, client *http.Client, now func() time.Time) (*Sender, error) {
	raw, err := decode(privateKey)
	if err != nil {
		return nil, fmt.Errorf("vapid key: %w", err)
	}
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, fmt.Errorf("vapid key: %w", err)
	}
	public, err := key.PublicKey.Bytes()
	if err != nil {
		return nil, fmt.Errorf("vapid key: %w", err)
	}
	if !strings.HasPrefix(subject, "mailto:") && !strings.HasPrefix(subject, "https://") {
		return nil, errors.New("vapid subject must be a mailto: or https:// address")
	}
	return &Sender{key: key, public: public, subject: subject, client: client, now: now}, nil
}

// GenerateKey makes a new VAPID private key, base64url-encoded.
func GenerateKey() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	raw, err := key.Bytes()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// PublicKey is what the dashboard passes to the browser as applicationServerKey.
func (s *Sender) PublicKey() string { return base64.RawURLEncoding.EncodeToString(s.public) }

// pushHosts are the browser push services. The endpoint comes from the browser, so it is only ever sent to these,
// over HTTPS: Greyship never makes requests to an address a user chose.
var pushHosts = [...]string{
	"fcm.googleapis.com",
	"updates.push.services.mozilla.com",
	".push.apple.com",
	".notify.windows.com",
}

// CheckEndpoint reports whether a subscription's endpoint is a known push service.
func CheckEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return errors.New("push endpoint must be an https URL of a browser push service")
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h || strings.HasPrefix(h, ".") && strings.HasSuffix(host, h) {
			return nil
		}
	}
	return fmt.Errorf("push endpoint host %s isn't a known browser push service", host)
}

// Send delivers payload (at most about 3KB) to one browser.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte) error {
	if err := CheckEndpoint(sub.Endpoint); err != nil {
		return err
	}
	return s.deliver(ctx, sub, payload)
}

func (s *Sender) deliver(ctx context.Context, sub Subscription, payload []byte) error {
	body, err := encrypt(sub, payload)
	if err != nil {
		return err
	}
	auth, err := s.authorization(sub.Endpoint)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "normal")
	req.Header.Set("Authorization", auth)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send push: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("push service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// authorization is the VAPID header: a short-lived ES256 JWT for the push service's origin, plus our public key.
func (s *Sender) authorization(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": s.now().Add(12 * time.Hour).Unix(), "sub": s.subject})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, sigS, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sigS.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + base64.RawURLEncoding.EncodeToString(sig) + ", k=" + s.PublicKey(), nil
}

// encrypt makes the aes128gcm body of RFC 8291: a fresh key pair and salt per message, one record.
func encrypt(sub Subscription, payload []byte) ([]byte, error) {
	browserKey, err := decode(sub.P256dh)
	if err != nil {
		return nil, fmt.Errorf("subscription key: %w", err)
	}
	authSecret, err := decode(sub.Auth)
	if err != nil || len(authSecret) != 16 {
		return nil, errors.New("subscription auth secret must be 16 bytes")
	}
	browser, err := ecdh.P256().NewPublicKey(browserKey)
	if err != nil {
		return nil, fmt.Errorf("subscription key: %w", err)
	}
	local, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := local.ECDH(browser)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return seal(shared, authSecret, browserKey, local.PublicKey().Bytes(), salt, payload)
}

func seal(shared, authSecret, browserKey, localKey, salt, payload []byte) ([]byte, error) {
	info := append(append([]byte("WebPush: info\x00"), browserKey...), localKey...)
	prk, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prk, string(info), 32)
	if err != nil {
		return nil, err
	}
	contentPRK, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, contentPRK, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, contentPRK, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// 0x02 marks the last (only) record; no padding.
	sealed := gcm.Seal(nil, nonce, append(append([]byte(nil), payload...), 0x02), nil)

	out := make([]byte, 0, 16+4+1+len(localKey)+len(sealed))
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, 4096)
	out = append(out, 65) // the length of an uncompressed P-256 key, which localKey always is
	out = append(out, localKey...)
	return append(out, sealed...), nil
}

func decode(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return base64.RawStdEncoding.DecodeString(s)
	}
	return b, nil
}
