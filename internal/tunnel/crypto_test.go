//go:build !integration

package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestCryptoRoundTrip(t *testing.T) {
	token := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6"
	c, err := NewCrypto(token)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte(`{"type":"message","data":{"text":"hello"}}`)
	nonce, ciphertext, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("nonce: %s", nonce)
	t.Logf("ciphertext: %s", ciphertext)

	// Simulate relay message format
	relayMsg := map[string]string{
		"type":       "encrypted",
		"nonce":      nonce,
		"ciphertext": ciphertext,
	}
	msgBytes, _ := json.Marshal(relayMsg)
	t.Logf("relay message: %s", string(msgBytes))

	// Parse and decrypt
	var parsed struct {
		Type       string `json:"type"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	json.Unmarshal(msgBytes, &parsed)

	decrypted, err := c.Decrypt(parsed.Nonce, parsed.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("mismatch: got %s", decrypted)
	}
}

func TestCryptoShortToken(t *testing.T) {
	// Token < 16 bytes triggers argon2 derivation
	c, err := NewCrypto("short")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("test data")
	nonce, ct, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Errorf("mismatch: got %q, want %q", dec, plain)
	}
}

func TestCrypto16ByteKey(t *testing.T) {
	// Exactly 16 bytes -> AES-128
	c, err := NewCrypto("0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("aes-128 test")
	nonce, ct, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Errorf("mismatch: got %q", dec)
	}
}

func TestCrypto24ByteKey(t *testing.T) {
	// Exactly 24 bytes -> AES-192
	c, err := NewCrypto("0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("aes-192 test")
	nonce, ct, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Errorf("mismatch: got %q", dec)
	}
}

func TestCrypto32ByteKey(t *testing.T) {
	// Exactly 32 bytes -> AES-256
	c, err := NewCrypto("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("aes-256 test")
	nonce, ct, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Errorf("mismatch: got %q", dec)
	}
}

func TestCryptoLongTokenTruncated(t *testing.T) {
	// Token > 32 bytes gets truncated to 32
	token := "0123456789abcdef0123456789abcdef_extrabytes"
	c, err := NewCrypto(token)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("truncated key test")
	nonce, ct, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Errorf("mismatch: got %q", dec)
	}
}

func TestCryptoDecryptInvalidNonce(t *testing.T) {
	c, err := NewCrypto("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	_, ct, _ := c.Encrypt([]byte("test"))
	_, err = c.Decrypt("invalid-base64!!!", ct)
	if err == nil {
		t.Error("expected error for invalid nonce base64")
	}
}

func TestCryptoDecryptInvalidCiphertext(t *testing.T) {
	c, err := NewCrypto("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	nonce, _, _ := c.Encrypt([]byte("test"))
	_, err = c.Decrypt(nonce, "invalid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid ciphertext base64")
	}
}

func TestCryptoDecryptWrongKey(t *testing.T) {
	c1, err := NewCrypto("key1-key1-key1-key1-key1-key1-ka")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := NewCrypto("key2-key2-key2-key2-key2-key2-kb")
	if err != nil {
		t.Fatal(err)
	}
	nonce, ct, err := c1.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c2.Decrypt(nonce, ct)
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

func TestCryptoDecryptTamperedCiphertext(t *testing.T) {
	c, _ := NewCrypto("0123456789abcdef0123456789abcdef")
	nonce, ct, _ := c.Encrypt([]byte("secret"))
	// Decode, tamper first byte, re-encode
	ctBytes, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctBytes) > 0 {
		ctBytes[0] ^= 0xFF
	}
	tampered := base64.StdEncoding.EncodeToString(ctBytes)
	_, err = c.Decrypt(nonce, tampered)
	if err == nil {
		t.Error("expected error for tampered ciphertext")
	}
}

func TestCryptoEmptyPlaintext(t *testing.T) {
	c, _ := NewCrypto("0123456789abcdef0123456789abcdef")
	nonce, ct, err := c.Encrypt([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec) != 0 {
		t.Errorf("expected empty plaintext, got %q", dec)
	}
}

func TestCryptoLargePlaintext(t *testing.T) {
	c, _ := NewCrypto("0123456789abcdef0123456789abcdef")
	plain := make([]byte, 10000)
	for i := range plain {
		plain[i] = byte(i % 256)
	}
	nonce, ct, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decrypt(nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Error("large plaintext mismatch")
	}
}

func TestCryptoDifferentNonceEachEncrypt(t *testing.T) {
	c, _ := NewCrypto("0123456789abcdef0123456789abcdef")
	plain := []byte("same data")
	n1, _, _ := c.Encrypt(plain)
	n2, _, _ := c.Encrypt(plain)
	if n1 == n2 {
		t.Error("nonce should be different for each encryption")
	}
}

func TestCryptoRoundTripViaJSON(t *testing.T) {
	c, _ := NewCrypto("test-token-that-is-32-bytes-long!")
	msg := GatewayMessage{Type: "message", EventID: "ev-1", Data: json.RawMessage(`{"text":"hi"}`)}

	plaintext, _ := json.Marshal(msg)
	nonce, ct, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate relay message
	relayMsg := map[string]string{
		"type":       "encrypted",
		"nonce":      nonce,
		"ciphertext": ct,
	}
	wire, _ := json.Marshal(relayMsg)

	// Parse and decrypt
	var parsed struct {
		Type       string `json:"type"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	json.Unmarshal(wire, &parsed)

	dec, err := c.Decrypt(parsed.Nonce, parsed.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	var gotMsg GatewayMessage
	json.Unmarshal(dec, &gotMsg)
	if gotMsg.Type != "message" || gotMsg.EventID != "ev-1" {
		t.Errorf("mismatch: %+v", gotMsg)
	}
}
