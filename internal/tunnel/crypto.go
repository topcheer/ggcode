package tunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/crypto/argon2"
	"strings"
)

type Crypto struct {
	aead cipher.AEAD
}

func NewCrypto(tokenHex string) (*Crypto, error) {

	if strings.TrimSpace(tokenHex) == "" {
		return nil, errors.New("crypto: empty session token")
	}

	key := []byte(tokenHex)

	if len(key) < 16 {

		salt := sha256.Sum256(append([]byte("ggcode-tunnel-salt-v1"), key...))
		derived := argon2.IDKey(key, salt[:16], 1, 64*1024, 4, 32)
		key = derived
	} else if len(key) > 32 {
		key = key[:32]
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	return &Crypto{aead: aead}, nil
}

func (c *Crypto) Encrypt(plaintext []byte) (nonce string, ciphertext string, err error) {
	nonceBytes := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", "", err
	}
	sealed := c.aead.Seal(nil, nonceBytes, plaintext, nil)
	return base64.StdEncoding.EncodeToString(nonceBytes),
		base64.StdEncoding.EncodeToString(sealed),
		nil
}

func (c *Crypto) Decrypt(nonceB64, ciphertextB64 string) ([]byte, error) {
	nonceBytes, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	return c.aead.Open(nil, nonceBytes, ciphertextBytes, nil)
}
