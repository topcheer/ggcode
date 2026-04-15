package im

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// --- pc_crypto ---

func TestPCGenerateAndDecodeSessionKey(t *testing.T) {
	key, err := pcGenerateSessionKey()
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Error("expected non-empty key")
	}

	decoded, err := pcDecodeSessionKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(decoded))
	}
}

func TestPCDecodeSessionKey_InvalidBase64(t *testing.T) {
	_, err := pcDecodeSessionKey("!!!invalid!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestPCDecodeSessionKey_WrongLength(t *testing.T) {
	// base64url of a single byte
	_, err := pcDecodeSessionKey("AA")
	if err == nil {
		t.Error("expected error for wrong length")
	}
}

func TestPCEncryptDecryptRoundTrip(t *testing.T) {
	sessionKey, _ := pcGenerateSessionKey()
	sessionID := "test-session-123"

	payload := pcPayload{
		"kind":  "user_message",
		"text":  "Hello, World!",
		"appId": "app-1",
	}

	envelope, err := pcEncryptPayload(sessionID, sessionKey, payload)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Version != 1 {
		t.Errorf("expected version 1, got %d", envelope.Version)
	}
	if envelope.MessageID == "" {
		t.Error("expected non-empty messageID")
	}
	if envelope.IV == "" || envelope.Ciphertext == "" || envelope.Tag == "" {
		t.Error("expected non-empty IV/Ciphertext/Tag")
	}

	decrypted, err := pcDecryptPayload(sessionID, sessionKey, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if pcPayloadKind(decrypted) != "user_message" {
		t.Errorf("expected kind=user_message, got %q", pcPayloadKind(decrypted))
	}
	if pcPayloadString(decrypted, "text") != "Hello, World!" {
		t.Errorf("expected text='Hello, World!', got %q", pcPayloadString(decrypted, "text"))
	}
}

func TestPCDecryptPayload_BadVersion(t *testing.T) {
	sessionKey, _ := pcGenerateSessionKey()
	envelope := &pcEncryptedEnvelope{Version: 99}
	_, err := pcDecryptPayload("sid", sessionKey, envelope)
	if err == nil {
		t.Error("expected error for bad version")
	}
}

func TestPCDecryptPayload_BadIV(t *testing.T) {
	sessionKey, _ := pcGenerateSessionKey()
	envelope := &pcEncryptedEnvelope{
		Version:    1,
		IV:         "!!!bad!!!",
		Ciphertext: "AA",
		Tag:        "AA",
	}
	_, err := pcDecryptPayload("sid", sessionKey, envelope)
	if err == nil {
		t.Error("expected error for bad IV")
	}
}

func TestPCDecryptPayload_WrongKey(t *testing.T) {
	key1, _ := pcGenerateSessionKey()
	key2, _ := pcGenerateSessionKey()

	envelope, _ := pcEncryptPayload("sid", key1, pcPayload{"kind": "test"})
	_, err := pcDecryptPayload("sid", key2, envelope)
	if err == nil {
		t.Error("expected error for wrong key")
	}
}

func TestPCDecryptPayload_WrongSessionID(t *testing.T) {
	key, _ := pcGenerateSessionKey()
	envelope, _ := pcEncryptPayload("sid-correct", key, pcPayload{"kind": "test"})
	_, err := pcDecryptPayload("sid-wrong", key, envelope)
	if err == nil {
		t.Error("expected error for wrong session ID")
	}
}

// --- pc_invite ---

func TestPCEncodeInviteToURI(t *testing.T) {
	invite := PCInvite{
		Version:    1,
		SessionID:  "sess-123",
		SessionKey: "key-abc",
		AppWsURL:   "ws://localhost:8080",
		ExpiresAt:  "2025-12-31T23:59:59Z",
	}

	uri, err := PCEncodeInviteToURI(invite)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, PCInviteURIPrefix) {
		t.Errorf("expected URI prefix %q, got %q", PCInviteURIPrefix, uri)
	}
}

func TestPCDecodeInvite_URI(t *testing.T) {
	invite := PCInvite{
		Version:    1,
		SessionID:  "sess-123",
		SessionKey: "key-abc",
	}
	uri, _ := PCEncodeInviteToURI(invite)

	decoded, err := pcDecodeInviteString(uri)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SessionID != "sess-123" {
		t.Errorf("expected SessionID=sess-123, got %q", decoded.SessionID)
	}
}

func TestPCDecodeInvite_JSON(t *testing.T) {
	input := `{"version":1,"sessionId":"json-sess","sessionKey":"json-key"}`
	decoded, err := pcDecodeInviteString(input)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SessionID != "json-sess" {
		t.Errorf("expected json-sess, got %q", decoded.SessionID)
	}
}

func TestPCDecodeInvite_InvalidURI(t *testing.T) {
	_, err := pcDecodeInviteString("privateclaw://connect?noplayload=bad")
	if err == nil {
		t.Error("expected error for missing payload")
	}
}

func TestPCDecodeInvite_InvalidBase64(t *testing.T) {
	_, err := pcDecodeInviteString("!!!not-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestPCDecodeInvite_InvalidJSON(t *testing.T) {
	_, err := pcDecodeInviteString("{bad json}")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestPCBuildMessageID(t *testing.T) {
	id := pcBuildMessageID("test")
	if !strings.HasPrefix(id, "test-") {
		t.Errorf("expected prefix 'test-', got %q", id)
	}
}

func TestPCNowISO(t *testing.T) {
	result := pcNowISO()
	if result == "" {
		t.Error("expected non-empty ISO timestamp")
	}
	// Should be parseable
	_, err := time.Parse("2006-01-02T15:04:05.000Z", result)
	if err != nil {
		t.Errorf("expected valid ISO timestamp, got %q: %v", result, err)
	}
}

// --- pc_protocol ---

func TestPCPayloadAccessors(t *testing.T) {
	p := pcPayload{
		"kind":  "user_message",
		"text":  "hello",
		"flag":  true,
		"count": float64(42),
	}

	if pcPayloadKind(p) != "user_message" {
		t.Errorf("kind = %q", pcPayloadKind(p))
	}
	if pcPayloadString(p, "text") != "hello" {
		t.Errorf("text = %q", pcPayloadString(p, "text"))
	}
	if !pcPayloadBool(p, "flag") {
		t.Error("flag should be true")
	}
	if pcPayloadInt(p, "count") != 42 {
		t.Errorf("count = %d", pcPayloadInt(p, "count"))
	}

	// Missing keys
	if pcPayloadString(p, "missing") != "" {
		t.Error("expected empty for missing string")
	}
	if pcPayloadBool(p, "missing") {
		t.Error("expected false for missing bool")
	}
	if pcPayloadInt(p, "missing") != 0 {
		t.Error("expected 0 for missing int")
	}

	// Int from actual int (not float64)
	p["int_val"] = 99
	if pcPayloadInt(p, "int_val") != 99 {
		t.Errorf("int_val = %d", pcPayloadInt(p, "int_val"))
	}
}

// --- pc_session ---

func TestPCSession_Lifecycle(t *testing.T) {
	invite := PCInvite{
		Version:    1,
		SessionID:  "sess-1",
		SessionKey: "key-1",
	}
	sess := newPCSession(invite, "test-label", false, time.Now().Add(time.Hour))

	if sess.IsExpired() {
		t.Error("session should not be expired")
	}
	if sess.IsActive() {
		t.Error("session should not be active (still awaiting hello)")
	}

	sess.SetActive()
	if !sess.IsActive() {
		t.Error("session should be active after SetActive")
	}
}

func TestPCSession_Expired(t *testing.T) {
	invite := PCInvite{Version: 1, SessionID: "exp"}
	sess := newPCSession(invite, "", false, time.Now().Add(-time.Hour))

	if !sess.IsExpired() {
		t.Error("session should be expired")
	}
}

func TestPCSession_Participants(t *testing.T) {
	sess := newPCSession(PCInvite{Version: 1}, "", true, time.Now().Add(time.Hour))

	sess.UpsertParticipant("app1", "Alice", "Mac")
	if sess.ParticipantCount() != 1 {
		t.Errorf("expected 1 participant, got %d", sess.ParticipantCount())
	}

	// Upsert same - update
	sess.UpsertParticipant("app1", "Bob", "PC")
	if sess.ParticipantCount() != 1 {
		t.Errorf("expected 1 participant after upsert, got %d", sess.ParticipantCount())
	}

	// Remove
	sess.MarkAppRemoved("app1")
	if !sess.IsAppRemoved("app1") {
		t.Error("app1 should be removed")
	}
	if sess.ParticipantCount() != 0 {
		t.Errorf("expected 0 participants after removal, got %d", sess.ParticipantCount())
	}
}

func TestPCSession_History(t *testing.T) {
	sess := newPCSession(PCInvite{Version: 1}, "", false, time.Now().Add(time.Hour))

	for i := 0; i < 5; i++ {
		sess.AppendHistory(pcConversationTurn{
			MessageID: "msg-" + string(rune('0'+i)),
			Role:      "user",
			Text:      "message " + string(rune('0'+i)),
		})
	}

	recent := sess.RecentHistory(3)
	if len(recent) != 3 {
		t.Errorf("expected 3, got %d", len(recent))
	}
	if recent[0].MessageID != "msg-2" {
		t.Errorf("expected msg-2, got %q", recent[0].MessageID)
	}

	// All history
	all := sess.RecentHistory(0)
	if len(all) != 5 {
		t.Errorf("expected 5, got %d", len(all))
	}
}

// --- pc_adapter resolve URLs ---

func TestPCResolveRelayWSURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ws://relay.example.com", "ws://relay.example.com"},
		{"wss://relay.example.com/path", "wss://relay.example.com/path"},
		{"http://relay.example.com", "ws://relay.example.com/ws/provider"},
		{"https://relay.example.com", "wss://relay.example.com/ws/provider"},
		{"", "wss://relay.privateclaw.us/ws/provider"},
	}
	for _, tc := range tests {
		got := pcResolveRelayWSURL(tc.input)
		if got != tc.want {
			t.Errorf("pcResolveRelayWSURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPCResolveAppWsURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "wss://relay.privateclaw.us/ws/app"},
		{"ws://app.example.com", "ws://app.example.com"},
		{"wss://app.example.com", "wss://app.example.com"},
		{"http://app.example.com", "ws://app.example.com/ws/app"},
		{"https://app.example.com", "wss://app.example.com/ws/app"},
	}
	for _, tc := range tests {
		got := pcResolveAppWsURL(tc.input)
		if got != tc.want {
			t.Errorf("pcResolveAppWsURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- pc_adapter payloadKindToRole ---

func TestPCPayloadKindToRole(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{pcKindUserMessage, "user"},
		{pcKindAssistantMessage, "assistant"},
		{pcKindSystemMessage, "system"},
		{"unknown", "unknown"},
	}
	for _, tc := range tests {
		got := payloadKindToRole(tc.kind)
		if got != tc.want {
			t.Errorf("payloadKindToRole(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// --- JSON round-trip for protocol types ---

func TestPCEncryptedEnvelopeJSON(t *testing.T) {
	env := pcEncryptedEnvelope{
		Version:    1,
		MessageID:  "msg-123",
		IV:         "iv-base64",
		Ciphertext: "ct-base64",
		Tag:        "tag-base64",
		SentAt:     "2025-01-01T00:00:00.000Z",
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pcEncryptedEnvelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.MessageID != "msg-123" {
		t.Errorf("expected msg-123, got %q", decoded.MessageID)
	}
}

func TestPCRelayErrorJSON(t *testing.T) {
	err := pcRelayError{
		Type:    pcTypeError,
		Code:    "session_not_found",
		Message: "session does not exist",
	}
	data, _ := json.Marshal(err)
	if !strings.Contains(string(data), "session_not_found") {
		t.Errorf("expected code in JSON: %s", data)
	}
}

// --- PC attachment resolution tests ---

func TestPCResolveAttachment_DataURL(t *testing.T) {
	adapter := &pcAdapter{}
	img := ExtractedImage{
		Kind: "data_url",
		Data: "data:image/png;base64,iVBORw0KGgo=",
	}
	att, err := adapter.resolvePCAttachment(context.Background(), img, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att["mimeType"] != "image/png" {
		t.Errorf("mimeType = %v", att["mimeType"])
	}
	if att["dataBase64"] == nil || att["dataBase64"] == "" {
		t.Error("expected non-empty dataBase64")
	}
	if att["sizeBytes"] == nil {
		t.Error("expected sizeBytes")
	}
}

func TestPCResolveAttachment_JPEGDataURL(t *testing.T) {
	adapter := &pcAdapter{}
	img := ExtractedImage{
		Kind: "data_url",
		Data: "data:image/jpeg;base64,/9j/4AAQ",
	}
	att, err := adapter.resolvePCAttachment(context.Background(), img, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att["mimeType"] != "image/jpeg" {
		t.Errorf("mimeType = %v", att["mimeType"])
	}
	name, _ := att["name"].(string)
	if name != "image_1.jpg" {
		t.Errorf("name = %q", name)
	}
}

func TestPCResolveAttachment_InvalidDataURL(t *testing.T) {
	adapter := &pcAdapter{}
	img := ExtractedImage{
		Kind: "data_url",
		Data: "invalid",
	}
	_, err := adapter.resolvePCAttachment(context.Background(), img, 0)
	if err == nil {
		t.Error("expected error for invalid data URL")
	}
}

func TestPCResolveAttachment_UnknownKind(t *testing.T) {
	adapter := &pcAdapter{}
	img := ExtractedImage{Kind: "unknown", Data: "test"}
	_, err := adapter.resolvePCAttachment(context.Background(), img, 0)
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestPCNewAdapter_DefaultValues(t *testing.T) {
	adapter, err := newPCAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{},
	}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.providerLabel != "ggcode" {
		t.Errorf("providerLabel = %q", adapter.providerLabel)
	}
	if adapter.welcomeMessage != "Connected to ggcode" {
		t.Errorf("welcomeMessage = %q", adapter.welcomeMessage)
	}
}
