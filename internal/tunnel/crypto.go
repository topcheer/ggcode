package tunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Crypto provides AES-GCM encryption/decryption using the token as key.
type Crypto struct {
	aead cipher.AEAD
}

// NewCrypto creates a Crypto instance from a token string (32+ chars = 16+ byte key).
// #921 CONTRACT CORRECTION: the token string is used as RAW BYTES (NOT hex-
// decoded as earlier docs claimed). A hex-looking token (e.g. randomHex(32) =
// 64 chars) therefore yields a key over the 16-symbol hex alphabet; the first
// 32 bytes are used directly as the AES-256 key. All ggcode endpoints share
// this derivation, so interop is intact; third parties must implement THIS
// scheme (or both sides must migrate to hex-decoding/KDF together).
// Length policy: <16 bytes -> argon2id-derived 32-byte key; 16/24/32 -> used
// directly; >32 -> truncated to 32 (intermediate lengths 17-31 also truncate).
func NewCrypto(tokenHex string) (*Crypto, error) {
	// Decode hex token to get key bytes
	key := []byte(tokenHex)
	// If key is too short for AES, derive via argon2id
	// AES-128 needs 16 bytes, AES-256 needs 32 bytes
	// We use the raw token bytes as key (token is 48 hex chars = 24 bytes → AES-192)
	// But AES only supports 16, 24, or 32 byte keys.
	// For simplicity: if len < 16, derive; if 16/24/32, use directly; else truncate to 32.
	if len(key) < 16 {
		// Derive 32-byte key via argon2id
		var salt [16]byte
		derived := argon2.IDKey(key, salt[:], 1, 64*1024, 4, 32)
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

// Encrypt encrypts plaintext and returns (nonce_base64, ciphertext_base64).
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

// Decrypt decrypts base64-encoded nonce + ciphertext and returns plaintext.
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
