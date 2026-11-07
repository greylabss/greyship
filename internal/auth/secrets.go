package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Signer makes tamper-proof, expiring values for short-lived cookies (OAuth state).
type Signer struct {
	Key []byte
	Now func() time.Time
}

var ErrBadSignature = errors.New("invalid or expired signed value")

type signed struct {
	Data    json.RawMessage `json:"d"`
	Expires int64           `json:"e"`
}

func (s Signer) Sign(v any, ttl time.Duration) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode signed value: %w", err)
	}
	body, err := json.Marshal(signed{Data: data, Expires: s.Now().Add(ttl).Unix()})
	if err != nil {
		return "", fmt.Errorf("encode signed value: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + s.mac(payload), nil
}

func (s Signer) Verify(raw string, v any) error {
	payload, mac, ok := strings.Cut(raw, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(s.mac(payload))) {
		return ErrBadSignature
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return ErrBadSignature
	}
	var sv signed
	if err := json.Unmarshal(body, &sv); err != nil || s.Now().Unix() > sv.Expires {
		return ErrBadSignature
	}
	if err := json.Unmarshal(sv.Data, v); err != nil {
		return ErrBadSignature
	}
	return nil
}

func (s Signer) mac(payload string) string {
	h := hmac.New(sha256.New, s.Key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
