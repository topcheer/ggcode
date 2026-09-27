package im

// r181 feishu domain seams: pins for the pure predicates and phase seams
// extracted from the feishu webhook cluster (handleWebhook / handleWebhook
// phases / parseMessageContent / sendExtractedImage / shared inbound tail).
// Behavior-preservation proof additionally rests on the pre-existing
// zz_issue955 / zz_issue2110 / zz_issue2309 / zz_issue1550 (source pin) /
// feishu_adapter_unit tests, which pass unchanged.

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// feishuSealTest encrypts plaintext with the official #2110 scheme
// (AES-256-CBC, key = sha256(encrypt_key), IV = key[:16], PKCS7).
func feishuSealTest(t *testing.T, plaintext, encryptKey string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append([]byte(plaintext), bytes.Repeat([]byte{byte(pad)}, pad)...)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, digest[:aes.BlockSize]).CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func TestFeishuDedupKeyPrefersEventID(t *testing.T) {
	payload := map[string]any{
		"header": map[string]any{"event_id": "evt-1"},
		"event":  map[string]any{"message": map[string]any{"message_id": "om-1"}},
	}
	if got := feishuDedupKey(payload); got != "evt-1" {
		t.Fatalf("header.event_id must win, got %q", got)
	}
	header := payload["header"].(map[string]any)
	delete(header, "event_id")
	if got := feishuDedupKey(payload); got != "om-1" {
		t.Fatalf("nested message_id fallback expected, got %q", got)
	}
	if got := feishuDedupKey(map[string]any{}); got != "" {
		t.Fatalf("empty payload must yield empty dedup key, got %q", got)
	}
	if got := feishuDedupKey(map[string]any{"header": nil}); got != "" {
		t.Fatalf("nil header must yield empty dedup key, got %q", got)
	}
}

func TestFeishuRemoteImageFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://cdn.example.com/a/b/pic.png", "pic.png"},
		{"https://cdn.example.com/dir/", "dir"}, // filepath.Base semantics preserved
		{"", "image.png"},                       // Base("") == "." → default
		{"https://x/y", "y"},
	}
	for _, tc := range cases {
		if got := remoteImageFilename(tc.in); got != tc.want {
			t.Errorf("remoteImageFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFeishuDataURLImage(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 250}
	enc := base64.StdEncoding.EncodeToString(payload)
	cases := []struct{ prefix, wantExt string }{
		{"data:image/png;base64", ".png"},
		{"data:image/jpeg;base64", ".jpg"},
		{"data:image/jpg;base64", ".jpg"},
		{"data:image/gif;base64", ".gif"},
		{"data:image/webp;base64", ".webp"},
		{"data:image/svg+xml;base64", ".png"}, // unrecognized → default
	}
	for _, tc := range cases {
		data, name, err := dataURLImage(tc.prefix + "," + enc)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.prefix, err)
		}
		if name != "image"+tc.wantExt {
			t.Errorf("%s: filename = %q, want %q", tc.prefix, name, "image"+tc.wantExt)
		}
		if !bytes.Equal(data, payload) {
			t.Errorf("%s: decoded payload mismatch", tc.prefix)
		}
	}
	if _, _, err := dataURLImage("no-comma-here"); err == nil || err.Error() != "invalid data URL" {
		t.Errorf("missing comma: err = %v, want invalid data URL", err)
	}
	if _, _, err := dataURLImage("data:image/png;base64,!!!not-base64!!!"); err == nil || !strings.Contains(err.Error(), "invalid base64") {
		t.Errorf("bad base64: err = %v, want invalid base64", err)
	}
}

func TestFeishuPostLanguageRank(t *testing.T) {
	cases := []struct {
		lang string
		want int
	}{{"zh_cn", 0}, {"en_us", 1}, {"fr", 2}, {"", 2}, {"zh", 2}}
	for _, tc := range cases {
		if got := postLanguageRank(tc.lang); got != tc.want {
			t.Errorf("postLanguageRank(%q) = %d, want %d", tc.lang, got, tc.want)
		}
	}
}

func TestFeishuPostLanguageTexts(t *testing.T) {
	langMap := map[string]any{
		"content": []any{
			[]any{
				map[string]any{"tag": "text", "text": "hello "},
				map[string]any{"tag": "at"}, // no text key → skipped
				"not-a-map",                 // non-map element → skipped
			},
			"not-an-array", // non-array line → skipped
			[]any{map[string]any{"text": "world"}},
		},
	}
	if got := postLanguageTexts(langMap); got != "hello world" {
		t.Fatalf("postLanguageTexts = %q, want %q", got, "hello world")
	}
	if got := postLanguageTexts(map[string]any{"content": "oops"}); got != "" {
		t.Fatalf("non-array content must yield empty, got %q", got)
	}
	if got := postLanguageTexts(map[string]any{}); got != "" {
		t.Fatalf("missing content must yield empty, got %q", got)
	}
}

// TestFeishuBestPostTextPreferenceOrder pins #2309: zh_cn wins over en_us
// over any other language regardless of map iteration order; empty blocks
// never win; no text at all yields "".
func TestFeishuBestPostTextPreferenceOrder(t *testing.T) {
	parsed := map[string]any{
		"fr":    map[string]any{"content": []any{[]any{map[string]any{"text": "bonjour"}}}},
		"en_us": map[string]any{"content": []any{[]any{map[string]any{"text": "hello"}}}},
		"zh_cn": map[string]any{"content": []any{[]any{map[string]any{"text": "你好"}}}},
	}
	for i := 0; i < 20; i++ { // map order is random; ranks must dominate
		if got := bestPostText(parsed); got != "你好" {
			t.Fatalf("iteration %d: zh_cn must win, got %q", i, got)
		}
	}
	enFr := map[string]any{
		"fr":    parsed["fr"],
		"en_us": parsed["en_us"],
	}
	if got := bestPostText(enFr); got != "hello" {
		t.Fatalf("en_us must beat unranked language, got %q", got)
	}
	if got := bestPostText(map[string]any{"fr": parsed["fr"]}); got != "bonjour" {
		t.Fatalf("single unranked language must still be picked, got %q", got)
	}
	if got := bestPostText(map[string]any{
		"zh_cn": map[string]any{"content": []any{}},
		"en_us": map[string]any{"content": []any{}},
	}); got != "" {
		t.Fatalf("all-empty blocks must yield empty, got %q", got)
	}
	if got := bestPostText(map[string]any{"zh_cn": "not-a-map"}); got != "" {
		t.Fatalf("malformed language block must yield empty, got %q", got)
	}
}

func TestFeishuMergeVoiceText(t *testing.T) {
	cases := []struct{ text, voice, want string }{
		{"", "", ""},
		{"hi", "", "hi"},
		{"", "transcript", "transcript"},
		{"hi", "transcript", "hi\n\ntranscript"},
	}
	for _, tc := range cases {
		if got := mergeVoiceText(tc.text, tc.voice); got != tc.want {
			t.Errorf("mergeVoiceText(%q,%q) = %q, want %q", tc.text, tc.voice, got, tc.want)
		}
	}
}

func TestFeishuReadWebhookBodyRejectsGET(t *testing.T) {
	a := &feishuAdapter{name: "t"}
	rec := httptest.NewRecorder()
	if _, ok := a.readWebhookBody(rec, httptest.NewRequest(http.MethodGet, "/webhook", nil)); ok {
		t.Fatal("GET must be rejected")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestFeishuReadWebhookBodyCapsSize(t *testing.T) {
	a := &feishuAdapter{name: "t"}
	big := bytes.Repeat([]byte("a"), (1<<20)+1024)
	rec := httptest.NewRecorder()
	if _, ok := a.readWebhookBody(rec, httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(big))); ok {
		t.Fatal("oversized body must be rejected")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	rec = httptest.NewRecorder()
	body, ok := a.readWebhookBody(rec, httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{"ok":true}`)))
	if !ok || string(body) != `{"ok":true}` {
		t.Fatalf("small body must pass through, ok=%v body=%q", ok, body)
	}
}

// TestFeishuVerifyWebhookGuardsOrder pins the three encrypt-key gates in
// order: signature → fresh timestamp → unseen nonce, with their exact 401s.
func TestFeishuVerifyWebhookGuardsOrder(t *testing.T) {
	a := &feishuAdapter{name: "t", encryptKey: "key", seenNonces: map[string]time.Time{}}
	body := []byte(`{"x":1}`)
	sign := func(ts, nonce, key string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(ts + nonce + key + string(body)))
		return hex.EncodeToString(mac.Sum(nil))
	}
	newReq := func(ts, nonce, sig string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		req.Header.Set("X-Lark-Request-Timestamp", ts)
		req.Header.Set("X-Lark-Request-Nonce", nonce)
		req.Header.Set("X-Lark-Signature", sig)
		return req
	}
	now := fmt.Sprintf("%d", time.Now().Unix())

	// Bad signature → 401 "signature mismatch".
	rec := httptest.NewRecorder()
	if a.verifyWebhookGuards(rec, newReq(now, "n1", sign(now, "n1", "wrong")), body) {
		t.Fatal("bad signature must fail")
	}
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "signature mismatch") {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Stale timestamp (valid signature) → 401 "stale request".
	old := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())
	rec = httptest.NewRecorder()
	if a.verifyWebhookGuards(rec, newReq(old, "n2", sign(old, "n2", "key")), body) {
		t.Fatal("stale timestamp must fail")
	}
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "stale") {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Fresh + valid → all three gates pass.
	rec = httptest.NewRecorder()
	if !a.verifyWebhookGuards(rec, newReq(now, "n3", sign(now, "n3", "key")), body) {
		t.Fatalf("fresh signed request must pass, status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Replayed (ts,nonce) pair with valid signature → 401 "replayed request".
	rec = httptest.NewRecorder()
	if a.verifyWebhookGuards(rec, newReq(now, "n3", sign(now, "n3", "key")), body) {
		t.Fatal("replayed nonce must fail")
	}
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "replayed") {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestFeishuDecodeWebhookPayload(t *testing.T) {
	a := &feishuAdapter{name: "t", encryptKey: "key"}

	rec := httptest.NewRecorder()
	if _, ok := a.decodeWebhookPayload(rec, []byte("not-json")); ok {
		t.Fatal("invalid JSON must fail")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	payload, ok := a.decodeWebhookPayload(httptest.NewRecorder(), []byte(`{"challenge":"c"}`))
	if !ok || payload["challenge"] != "c" {
		t.Fatalf("plain JSON must pass through, ok=%v payload=%v", ok, payload)
	}

	// Official #2110 round trip: encrypted envelope unwraps in place.
	enc := feishuSealTest(t, `{"challenge":"deep"}`, "key")
	payload, ok = a.decodeWebhookPayload(httptest.NewRecorder(), []byte(`{"encrypt":"`+enc+`"}`))
	if !ok || payload["challenge"] != "deep" {
		t.Fatalf("encrypted challenge must unwrap, ok=%v payload=%v", ok, payload)
	}

	// Encrypted body without local encrypt_key → 400, never silent 200.
	a2 := &feishuAdapter{name: "t"}
	rec = httptest.NewRecorder()
	if _, ok := a2.decodeWebhookPayload(rec, []byte(`{"encrypt":"AAAA"}`)); ok {
		t.Fatal("encrypted body without key must fail")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFeishuRespondWebhookChallenge(t *testing.T) {
	rec := httptest.NewRecorder()
	if !respondWebhookChallenge(rec, map[string]any{"challenge": "ch-1"}) {
		t.Fatal("challenge payload must be answered")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response must be JSON: %v", err)
	}
	if resp["challenge"] != "ch-1" {
		t.Fatalf("challenge echo = %v, want ch-1", resp["challenge"])
	}

	rec = httptest.NewRecorder()
	if respondWebhookChallenge(rec, map[string]any{"header": map[string]any{}}) {
		t.Fatal("non-challenge payload must not be answered")
	}
}

func TestFeishuLoadLocalImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.png")
	if err := os.WriteFile(p, []byte("raw-bytes-not-validated-by-readlimited"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, name, err := loadLocalImage(p)
	if err != nil {
		t.Fatalf("loadLocalImage: %v", err)
	}
	if name != "hello.png" {
		t.Fatalf("filename = %q, want hello.png", name)
	}
	if string(data) != "raw-bytes-not-validated-by-readlimited" {
		t.Fatalf("data mismatch: %q", data)
	}
	if _, _, err := loadLocalImage(filepath.Join(dir, "missing.png")); err == nil || !strings.Contains(err.Error(), "read local image") {
		t.Fatalf("missing file: err = %v, want read local image", err)
	}
}
