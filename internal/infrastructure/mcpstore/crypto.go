package mcpstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type TokenCodec struct {
	aead cipher.AEAD
}

func NewTokenCodec(key string) (TokenCodec, error) {
	if len(key) != 32 {
		return TokenCodec{}, fmt.Errorf("mcp token encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return TokenCodec{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return TokenCodec{}, err
	}
	return TokenCodec{aead: aead}, nil
}

func (c TokenCodec) Encrypt(plaintext string) (string, error) {
	if c.aead == nil {
		return "", fmt.Errorf("mcp token codec is not initialized")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c TokenCodec) Decrypt(ciphertext string) (string, error) {
	if c.aead == nil {
		return "", fmt.Errorf("mcp token codec is not initialized")
	}
	raw, err := base64.RawStdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	if len(raw) < c.aead.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce := raw[:c.aead.NonceSize()]
	body := raw[c.aead.NonceSize():]
	opened, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(opened), nil
}
