// Package secrets encrypts environment variables at rest (spec F-41, architecture §5 Secrets). Each project has
// its own data key (DEK), stored only encrypted by the master key (KEK). Values are AES-256-GCM sealed with the
// DEK, bound to their project and key so a ciphertext can't be moved to another row. Only the node agent
// decrypts, at instance start, and `grey env pull` on explicit request.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const keySize = 32

var ErrDecrypt = errors.New("can't decrypt: wrong key or damaged data")

// Keyring holds the master key.
type Keyring struct {
	master cipher.AEAD
}

// NewKeyring takes the 32-byte master key as hex (openssl rand -hex 32).
func NewKeyring(masterHex string) (*Keyring, error) {
	key, err := hex.DecodeString(masterHex)
	if err != nil || len(key) != keySize {
		return nil, errors.New("the master key must be 32 bytes of hex (openssl rand -hex 32)")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &Keyring{master: aead}, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// NewProjectKey makes a project's data key and returns it wrapped by the master key, ready to store.
func (k *Keyring) NewProjectKey(projectID string) ([]byte, error) {
	dek := make([]byte, keySize)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("generate data key: %w", err)
	}
	return seal(k.master, dek, []byte("dek:"+projectID))
}

// ProjectKey unwraps a stored data key.
func (k *Keyring) ProjectKey(projectID string, wrapped []byte) (*ProjectKey, error) {
	dek, err := open(k.master, wrapped, []byte("dek:"+projectID))
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	return &ProjectKey{projectID: projectID, aead: aead}, nil
}

// ProjectKey encrypts and decrypts one project's values.
type ProjectKey struct {
	projectID string
	aead      cipher.AEAD
}

func (p *ProjectKey) ad(env, key string) []byte {
	return []byte("env:" + p.projectID + "\x00" + env + "\x00" + key)
}

func (p *ProjectKey) Encrypt(env, key, value string) ([]byte, error) {
	return seal(p.aead, []byte(value), p.ad(env, key))
}

func (p *ProjectKey) Decrypt(env, key string, ciphertext []byte) (string, error) {
	b, err := open(p.aead, ciphertext, p.ad(env, key))
	return string(b), err
}

// Hash is what the CLI and API compare to find changed values without sending them (decisions.md 020).
// It includes the project and key, so equal values elsewhere don't match.
func Hash(projectID, key, value string) string {
	sum := sha256.Sum256([]byte(projectID + "\x00" + key + "\x00" + value))
	return hex.EncodeToString(sum[:])
}

// seal returns nonce || ciphertext.
func seal(aead cipher.AEAD, plaintext, ad []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, ad), nil
}

func open(aead cipher.AEAD, sealed, ad []byte) ([]byte, error) {
	if len(sealed) < aead.NonceSize() {
		return nil, ErrDecrypt
	}
	b, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return b, nil
}
