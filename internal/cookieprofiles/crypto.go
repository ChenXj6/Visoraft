package cookieprofiles

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const sealedVersion byte = 1

type SecretBox struct {
	aead cipher.AEAD
}

func NewSecretBox(encodedKey string) (*SecretBox, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode cookie encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("cookie encryption key must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cookie encryption cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create cookie encryption AEAD: %w", err)
	}
	return &SecretBox{aead: aead}, nil
}

func (b *SecretBox) Seal(purpose string, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate cookie encryption nonce: %w", err)
	}
	packet := make([]byte, 1, 1+len(nonce)+len(plaintext)+b.aead.Overhead())
	packet[0] = sealedVersion
	packet = append(packet, nonce...)
	packet = b.aead.Seal(packet, nonce, plaintext, []byte(purpose))
	return packet, nil
}

func (b *SecretBox) Open(purpose string, packet []byte) ([]byte, error) {
	minimum := 1 + b.aead.NonceSize() + b.aead.Overhead()
	if len(packet) < minimum || packet[0] != sealedVersion {
		return nil, errors.New("encrypted cookie value has an unsupported format")
	}
	nonceEnd := 1 + b.aead.NonceSize()
	plaintext, err := b.aead.Open(
		nil,
		packet[1:nonceEnd],
		packet[nonceEnd:],
		[]byte(purpose),
	)
	if err != nil {
		return nil, errors.New("encrypted cookie value could not be authenticated")
	}
	return plaintext, nil
}
