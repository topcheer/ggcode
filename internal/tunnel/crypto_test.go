package tunnel

import (
	"encoding/json"
	"fmt"
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

	// Key info
	keyBytes := []byte(token)
	t.Logf("token len=%d, key=first 32 bytes", len(keyBytes))
	fmt.Printf("key hex: %x\n", keyBytes[:32])
}
