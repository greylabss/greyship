package secrets

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const master = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestRoundTripAndBinding(t *testing.T) {
	kr, err := NewKeyring(master)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := kr.NewProjectKey("p1")
	if err != nil {
		t.Fatal(err)
	}
	pk, err := kr.ProjectKey("p1", wrapped)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := pk.Encrypt("production", "STRIPE_KEY", "sk_live_123")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("sk_live")) {
		t.Fatal("the value is visible in its ciphertext")
	}
	if got, err := pk.Decrypt("production", "STRIPE_KEY", ct); err != nil || got != "sk_live_123" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	again, _ := pk.Encrypt("production", "STRIPE_KEY", "sk_live_123")
	if bytes.Equal(ct, again) {
		t.Fatal("two encryptions of one value must differ (fresh nonce)")
	}

	// A ciphertext only opens for the project, environment and key it was made for.
	for _, tt := range []struct{ env, key string }{{"preview", "STRIPE_KEY"}, {"production", "OTHER"}} {
		if _, err := pk.Decrypt(tt.env, tt.key, ct); !errors.Is(err, ErrDecrypt) {
			t.Errorf("moved to %s/%s: err = %v, want ErrDecrypt", tt.env, tt.key, err)
		}
	}
	if _, err := kr.ProjectKey("p2", wrapped); !errors.Is(err, ErrDecrypt) {
		t.Fatal("a data key opened for another project")
	}
	other, _ := NewKeyring(strings.Repeat("ff", 32))
	if _, err := other.ProjectKey("p1", wrapped); !errors.Is(err, ErrDecrypt) {
		t.Fatal("a data key opened with the wrong master key")
	}
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 1
	if _, err := pk.Decrypt("production", "STRIPE_KEY", tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatal("a tampered ciphertext decrypted")
	}
}

func TestBadMasterKey(t *testing.T) {
	for _, k := range []string{"", "zz", "0011"} {
		if _, err := NewKeyring(k); err == nil {
			t.Errorf("NewKeyring(%q) accepted a bad key", k)
		}
	}
}

func TestHash(t *testing.T) {
	a, b := Hash("p1", "K", "v"), Hash("p1", "K", "v")
	if a != b || a == Hash("p2", "K", "v") || a == Hash("p1", "J", "v") {
		t.Fatal("hashes must be stable and differ by project and key")
	}
}
