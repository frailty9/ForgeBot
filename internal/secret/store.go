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
)

var (
	ErrNotFound          = errors.New("secret not found")
	ErrCandidateNotFound = errors.New("secret candidate not found")
)

type Status struct {
	Configured bool      `json:"configured"`
	Version    uint64    `json:"version"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

type Candidate struct {
	Ref       string
	Version   uint64
	UpdatedAt time.Time
}

type Store interface {
	Status(context.Context, string) (Status, error)
	Get(context.Context, string) (string, error)
	Stage(context.Context, string, string) (Candidate, error)
	Activate(context.Context, Candidate) (Status, error)
	Discard(context.Context, Candidate) error
	Delete(context.Context, string) error
}

type Crypter struct{ aead cipher.AEAD }

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
