// Package crypto provides AES-256-GCM encryption for secrets at rest.
//
// Ciphertext layout: nonce (12 bytes) || GCM(plaintext). The additional
// authenticated data (AAD) binds a ciphertext to its owning row so a value
// copied between rows fails to decrypt.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (b *Box) Decrypt(ciphertext, aad []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("crypto: ciphertext too short")
	}
	return b.aead.Open(nil, ciphertext[:ns], ciphertext[ns:], aad)
}
