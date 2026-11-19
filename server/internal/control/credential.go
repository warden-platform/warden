package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

type credentialCipher struct {
	aead cipher.AEAD
}

func newCredentialCipher(secret []byte) (*credentialCipher, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("credential encryption key must be at least 32 bytes")
	}
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &credentialCipher{aead: aead}, nil
}

func (c *credentialCipher) Encrypt(value []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, c.aead.Seal(nil, nonce, value, nil)...), nil
}

func (c *credentialCipher) Decrypt(value []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(value) <= nonceSize {
		return nil, fmt.Errorf("invalid encrypted credential")
	}
	return c.aead.Open(nil, value[:nonceSize], value[nonceSize:], nil)
}
