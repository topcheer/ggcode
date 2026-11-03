package im

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func zz2110Encrypt(t *testing.T, plaintext, encryptKey string) string {
	t.Helper()
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append([]byte(plaintext), bytes.Repeat([]byte{byte(pad)}, pad)...)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func TestFeishuEncryptedWebhookRoundTrip(t *testing.T) {
	mgr := NewManager()
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {})
	a := newFeishu955Adapter(mgr, "")
	a.encryptKey = "test-encrypt-key"

	inner := map[string]any{"challenge": "ch-2110"}
	innerRaw, _ := json.Marshal(inner)
	enc := zz2110Encrypt(t, string(innerRaw), a.encryptKey)

	body, _ := json.Marshal(map[string]string{"encrypt": enc})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))

	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "n-2110"
	mac := hmac.New(sha256.New, []byte(a.encryptKey))
	mac.Write([]byte(ts + nonce + a.encryptKey + string(body)))
	req.Header.Set("X-Lark-Request-Timestamp", ts)
	req.Header.Set("X-Lark-Request-Nonce", nonce)
	req.Header.Set("X-Lark-Signature", hex.EncodeToString(mac.Sum(nil)))

	rec := httptest.NewRecorder()
	a.handleWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("encrypted challenge: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ch-2110") {
		t.Fatalf("challenge must be echoed from INSIDE the ciphertext, got: %s", rec.Body.String())
	}
}

func TestFeishuEncryptedWebhookNoKeyConfigured(t *testing.T) {
	mgr := NewManager()
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {})
	a := newFeishu955Adapter(mgr, "")
	a.encryptKey = ""

	body, _ := json.Marshal(map[string]string{"encrypt": "AAAA"})
	rec := httptest.NewRecorder()
	a.handleWebhook(rec, httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body)))
	if rec.Code == http.StatusOK {
		t.Fatalf("encrypted body with no local encrypt_key must surface (was: silent bare 200), got %d", rec.Code)
	}
}
