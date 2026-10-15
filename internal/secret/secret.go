// Package secret encrypts small values at rest with AES-256-GCM.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const version = 1

type Box struct{ aead cipher.AEAD }

// ParseKey accepts 32 random bytes encoded as base64 (std/url) or 64 hex chars.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("EPIC_SECRET_KEY must be 32 random bytes as base64 or hex (e.g. openssl rand -base64 32)")
}

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secret key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &Box{aead: a}, nil
}

// Seal returns version||nonce||ciphertext. aad binds the blob to its record.
func (b *Box) Seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{version}, nonce...)
	return b.aead.Seal(out, nonce, plain, aad), nil
}

func (b *Box) Open(blob, aad []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(blob) < 1+n+b.aead.Overhead() || blob[0] != version {
		return nil, errors.New("invalid encrypted value")
	}
	p, err := b.aead.Open(nil, blob[1:1+n], blob[1+n:], aad)
	if err != nil {
		return nil, errors.New("cannot decrypt value (wrong EPIC_SECRET_KEY?)")
	}
	return p, nil
}
