// Package secret stores API credentials encrypted at rest and exposes status
// and write-only lifecycle operations to management layers.
package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

var (
	ErrNotFound          = operation.New(operation.NotFound, "secret not found")
	ErrCandidateNotFound = operation.New(operation.NotFound, "secret candidate not found")
)

// Status exposes secret presence and version without revealing plaintext.
type Status struct {
	Configured bool      `json:"configured"`
	Version    uint64    `json:"version"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

// Candidate identifies a staged secret awaiting activation.
type Candidate struct {
	Ref       string
	Version   uint64
	UpdatedAt time.Time
}

// Store is the encrypted secret lifecycle boundary.
type Store interface {
	Status(context.Context, string) (Status, error)
	Get(context.Context, string) (string, error)
	Stage(context.Context, string, string) (Candidate, error)
	Activate(context.Context, Candidate) (Status, error)
	Discard(context.Context, Candidate) error
	Delete(context.Context, string) error
}

// Crypter encrypts and authenticates secret values with AES-256-GCM.
type Crypter struct{ aead cipher.AEAD }

// NewCrypter creates an AES-256-GCM crypter from a 32-byte key.
func NewCrypter(key []byte) (*Crypter, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypter{aead: aead}, nil
}

// ParseKey decodes the Base64 representation used by FORGEBOT_SECRET_KEY.
func ParseKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode secret key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secret key must decode to exactly 32 bytes")
	}
	return key, nil
}

func (c *Crypter) Encrypt(plain []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, nonce, plain, nil)
	payload := append(nonce, sealed...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func (c *Crypter) Decrypt(encoded string) ([]byte, error) {
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted secret: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(payload) < nonceSize {
		return nil, errors.New("encrypted secret is truncated")
	}
	plain, err := c.aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return nil, errors.New("decrypt secret: authentication failed")
	}
	return plain, nil
}
