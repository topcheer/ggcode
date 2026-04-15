package im

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"time"

	"github.com/gorilla/websocket"
)

// --- qqUploadPath ---

func TestQQUploadPath(t *testing.T) {
	tests := []struct {
		chatType string
		targetID string
		want     string
	}{
		{"c2c", "user123", "/v2/users/user123/files"},
		{"user", "user123", "/v2/users/user123/files"},
		{"dm", "dm456", "/v2/users/dm456/files"},
		{"group", "grp789", "/v2/groups/grp789/files"},
		{"groups", "grp789", "/v2/groups/grp789/files"},
		{"guild", "guild123", ""},
		{"", "target", ""},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s/%s", tc.chatType, tc.targetID), func(t *testing.T) {
			got := qqUploadPath(tc.chatType, tc.targetID)
			if got != tc.want {
				t.Errorf("qqUploadPath(%q, %q) = %q, want %q", tc.chatType, tc.targetID, got, tc.want)
			}
		})
	}
}

// --- uploadCacheKey ---

func TestUploadCacheKey(t *testing.T) {
	a := &qqAdapter{}
	key1 := a.uploadCacheKey("abc", "c2c", "user1", 1)
	key2 := a.uploadCacheKey("abc", "c2c", "user2", 1)
	key3 := a.uploadCacheKey("abc", "c2c", "user1", 1)
	if key1 == key2 {
		t.Error("different targetID should produce different keys")
	}
	if key1 != key3 {
		t.Error("same inputs should produce same key")
	}
}

// --- getUploadCache / setUploadCache ---

func TestUploadCacheSetGet(t *testing.T) {
	a := &qqAdapter{uploadCache: make(map[string]qqUploadCacheEntry)}

	// Cache miss
	if _, ok := a.getUploadCache("nonexistent"); ok {
		t.Error("expected cache miss")
	}

	// Cache set + hit
	a.setUploadCache("key1", "file_info_abc")
	got, ok := a.getUploadCache("key1")
	if !ok || got != "file_info_abc" {
		t.Errorf("expected cache hit with file_info_abc, got %q ok=%v", got, ok)
	}
}

func TestUploadCacheExpiration(t *testing.T) {
	a := &qqAdapter{uploadCache: make(map[string]qqUploadCacheEntry)}
	a.uploadCache["expired_key"] = qqUploadCacheEntry{
		FileInfo:  "old_info",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // expired
	}
	if _, ok := a.getUploadCache("expired_key"); ok {
		t.Error("expired entry should be a miss")
	}
}

func TestUploadCacheEviction(t *testing.T) {
	a := &qqAdapter{uploadCache: make(map[string]qqUploadCacheEntry)}
	// Fill beyond max
	for i := 0; i < qqUploadCacheMaxEntries+10; i++ {
		a.uploadCache[fmt.Sprintf("old_%d", i)] = qqUploadCacheEntry{
			FileInfo:  fmt.Sprintf("info_%d", i),
			ExpiresAt: time.Now().Add(-1 * time.Hour), // all expired
		}
	}
	a.setUploadCache("new_key", "new_info")
	got, ok := a.getUploadCache("new_key")
	if !ok || got != "new_info" {
		t.Errorf("after eviction, expected new_key to be set, got %q ok=%v", got, ok)
	}
}

// --- qqCloseReason ---

func TestQQCloseReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"close_4914", &websocket.CloseError{Code: 4914}, "bot is offline/sandbox-only"},
		{"close_4915", &websocket.CloseError{Code: 4915}, "bot is banned"},
		{"close_4004", &websocket.CloseError{Code: 4004}, "invalid token"},
		{"close_4006", &websocket.CloseError{Code: 4006}, "session invalid"},
		{"close_4007", &websocket.CloseError{Code: 4007}, "session invalid"},
		{"close_4008", &websocket.CloseError{Code: 4008}, "rate limited"},
		{"close_9999", &websocket.CloseError{Code: 9999, Text: "custom"}, "close code 9999: custom"},
		{"generic", fmt.Errorf("connection refused"), "connection refused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := qqCloseReason(tc.err)
			if got != tc.want {
				t.Errorf("qqCloseReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- sequence ---

func TestSequence(t *testing.T) {
	a := &qqAdapter{}
	if _, ok := a.sequence(); ok {
		t.Error("zero lastSeq should return false")
	}
	a.mu.Lock()
	a.lastSeq = 42
	a.mu.Unlock()
	seq, ok := a.sequence()
	if !ok || seq != 42 {
		t.Errorf("expected seq=42 ok=true, got seq=%d ok=%v", seq, ok)
	}
}

// --- currentHeartbeatEvery ---

func TestCurrentHeartbeatEvery(t *testing.T) {
	a := &qqAdapter{}
	if d := a.currentHeartbeatEvery(); d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
	a.mu.Lock()
	a.heartbeatEvery = 30 * time.Second
	a.mu.Unlock()
	if d := a.currentHeartbeatEvery(); d != 30*time.Second {
		t.Errorf("expected 30s, got %v", d)
	}
}

// --- qqPayloadKeys ---

func TestQQPayloadKeys(t *testing.T) {
	if got := qqPayloadKeys(nil); got != "-" {
		t.Errorf("nil = %q, want -", got)
	}
	if got := qqPayloadKeys(map[string]any{}); got != "-" {
		t.Errorf("empty = %q, want -", got)
	}
	got := qqPayloadKeys(map[string]any{"z": 1, "a": 2})
	if got != "a,z" {
		t.Errorf("got %q, want a,z", got)
	}
}

// --- truncateStr ---

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("abc", 5); got != "abc" {
		t.Errorf("short string should pass through: %q", got)
	}
	if got := truncateStr("abcdef", 5); got != "abcd…" {
		t.Errorf("long string should truncate: %q", got)
	}
	if got := truncateStr("", 5); got != "" {
		t.Errorf("empty should stay empty: %q", got)
	}
	if got := truncateStr("abc", 0); got != "abc" {
		t.Errorf("maxLen=0 should pass through: %q", got)
	}
}

// --- qqAttachmentExt ---

func TestQQAttachmentExt(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"application/pdf", ".pdf"},
		{"text/plain", ".txt"},
		{"application/json", ".json"},
		{"unknown/foo", ""},
		{"", ""},
		{"IMAGE/PNG", ".png"},
	}
	for _, tc := range tests {
		t.Run(tc.mime, func(t *testing.T) {
			got := qqAttachmentExt(tc.mime)
			if got != tc.want {
				t.Errorf("qqAttachmentExt(%q) = %q, want %q", tc.mime, got, tc.want)
			}
		})
	}
}

// --- resolveImageSource (data_url branch) ---

func TestResolveImageSource_DataURL(t *testing.T) {
	a := &qqAdapter{}

	// Create a valid 1x1 PNG
	b64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="
	dataURL := "data:image/png;base64," + b64

	img := ExtractedImage{Kind: "data_url", Data: dataURL}
	result, err := a.resolveImageSource(t.Context(), img)
	if err != nil {
		t.Fatalf("resolveImageSource data_url: %v", err)
	}
	if result != b64 {
		t.Error("should return raw base64 without data: prefix")
	}
}

func TestResolveImageSource_DataURLInvalidBase64(t *testing.T) {
	a := &qqAdapter{}
	img := ExtractedImage{Kind: "data_url", Data: "data:image/png;base64,!!!invalid!!!"}
	_, err := a.resolveImageSource(t.Context(), img)
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestResolveImageSource_DataURLInvalidImage(t *testing.T) {
	a := &qqAdapter{}
	b64 := base64.StdEncoding.EncodeToString([]byte("not an image"))
	img := ExtractedImage{Kind: "data_url", Data: "data:image/png;base64," + b64}
	_, err := a.resolveImageSource(t.Context(), img)
	if err == nil {
		t.Error("expected error for invalid image data")
	}
}

func TestResolveImageSource_LocalFile(t *testing.T) {
	a := &qqAdapter{}

	// Create a temp PNG file
	pngB64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="
	pngData, _ := base64.StdEncoding.DecodeString(pngB64)
	tmpFile := filepath.Join(t.TempDir(), "test.png")
	if err := os.WriteFile(tmpFile, pngData, 0644); err != nil {
		t.Fatal(err)
	}

	img := ExtractedImage{Kind: "url", Data: tmpFile}
	result, err := a.resolveImageSource(t.Context(), img)
	if err != nil {
		t.Fatalf("resolveImageSource local file: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty base64 result")
	}
}

func TestResolveImageSource_LocalFileNotFound(t *testing.T) {
	a := &qqAdapter{}
	img := ExtractedImage{Kind: "url", Data: "/nonexistent/path/image.png"}
	_, err := a.resolveImageSource(t.Context(), img)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestResolveImageSource_UnknownKind(t *testing.T) {
	a := &qqAdapter{}
	img := ExtractedImage{Kind: "unknown", Data: "whatever"}
	_, err := a.resolveImageSource(t.Context(), img)
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

// --- sendUnauthorized ---

func TestSendUnauthorized(t *testing.T) {
	adapter := &qqAdapter{connected: false}
	err := adapter.sendUnauthorized(t.Context(), "channel1", "")
	// Should not error when not connected (returns nil)
	if err != nil {
		t.Errorf("sendUnauthorized when not connected: %v", err)
	}
}

// --- outboundText (all branches) ---

func TestOutboundText_AllKinds(t *testing.T) {
	adapter := &qqAdapter{}
	tests := []struct {
		name  string
		event OutboundEvent
		want  string
	}{
		{"text", OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "hello"},
		{"status", OutboundEvent{Kind: OutboundEventStatus, Status: "running..."}, "running..."},
		{"approval", OutboundEvent{Kind: OutboundEventApprovalRequest, Approval: &ApprovalRequest{ToolName: "run_command", Input: "ls"}}, "[approval] run_command\nls"},
		{"approval_nil", OutboundEvent{Kind: OutboundEventApprovalRequest, Approval: nil}, ""},
		{"result", OutboundEvent{Kind: OutboundEventApprovalResult, Result: &ApprovalResult{Decision: permission.Allow}}, "[approval result] allow"},
		{"result_nil", OutboundEvent{Kind: OutboundEventApprovalResult, Result: nil}, ""},
		{"unknown", OutboundEvent{Kind: "unknown_kind"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := adapter.outboundText(tc.event)
			if got != tc.want {
				t.Errorf("outboundText() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- buildTextBody ---

func TestBuildTextBody(t *testing.T) {
	adapter := &qqAdapter{markdownSupport: true}
	body := adapter.buildTextBody("hello", "c2c", "", 0)
	if body["msg_type"] != qqMsgTypeMarkdown {
		t.Errorf("expected markdown msg_type, got %v", body["msg_type"])
	}
}

// --- boolValue (more branches) ---

func TestBoolValue_AllBranches(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		keys  []string
		def   bool
		want  bool
	}{
		{"bool_true", map[string]any{"flag": true}, []string{"flag"}, false, true},
		{"bool_false", map[string]any{"flag": false}, []string{"flag"}, true, false},
		{"string_true", map[string]any{"flag": "true"}, []string{"flag"}, false, true},
		{"string_yes", map[string]any{"flag": "yes"}, []string{"flag"}, false, true},
		{"string_1", map[string]any{"flag": "1"}, []string{"flag"}, false, true},
		{"string_on", map[string]any{"flag": "on"}, []string{"flag"}, false, true},
		{"string_false", map[string]any{"flag": "false"}, []string{"flag"}, true, false},
		{"string_no", map[string]any{"flag": "no"}, []string{"flag"}, true, false},
		{"string_0", map[string]any{"flag": "0"}, []string{"flag"}, true, false},
		{"string_off", map[string]any{"flag": "off"}, []string{"flag"}, true, false},
		{"missing_key", map[string]any{}, []string{"flag"}, true, true},
		{"fallback_key", map[string]any{"fallback": true}, []string{"primary", "fallback"}, false, true},
		{"def_true", map[string]any{}, []string{"flag"}, true, true},
		{"def_false", map[string]any{}, []string{"flag"}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := boolValue(tc.extra, tc.def, tc.keys...)
			if got != tc.want {
				t.Errorf("boolValue() = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- intValue (more branches) ---

func TestIntValue_AllTypes(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  int
		ok    bool
	}{
		{"int", 42, 42, true},
		{"int64", int64(100), 100, true},
		{"float64", float64(3.14), 3, true},
		{"nil", nil, 0, false},
		{"string", "42", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := intValue(tc.input)
			if got != tc.want || ok != tc.ok {
				t.Errorf("intValue(%v) = (%d, %v), want (%d, %v)", tc.input, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// --- isQQTokenExpiredPayload (full branches) ---

func TestIsQQTokenExpiredPayload(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		payload map[string]any
		want    bool
	}{
		{"ok_status", 200, nil, false},
		{"code_11244", 400, map[string]any{"code": 11244}, true},
		{"err_code_11244", 400, map[string]any{"err_code": 11244}, true},
		{"message_expired", 401, map[string]any{"message": "token not exist or expire"}, true},
		{"other_code", 400, map[string]any{"code": 99999}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isQQTokenExpiredPayload(tc.status, tc.payload)
			if got != tc.want {
				t.Errorf("isQQTokenExpiredPayload() = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- isQQVoiceAttachment (more branches) ---

func TestIsQQVoiceAttachment(t *testing.T) {
	tests := []struct {
		contentType string
		filename    string
		want        bool
	}{
		{"voice", "msg.silk", true},
		{"audio/wav", "", true},
		{"audio/mp3", "", true},
		{"", "recording.amr", true},
		{"", "audio.m4a", true},
		{"", "clip.ogg", true},
		{"", "sound.mp3", true},
		{"", "file.wav", true},
		{"image/png", "img.png", false},
		{"", "doc.pdf", false},
		{"", "", false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s/%s", tc.contentType, tc.filename), func(t *testing.T) {
			got := isQQVoiceAttachment(tc.contentType, tc.filename)
			if got != tc.want {
				t.Errorf("isQQVoiceAttachment(%q, %q) = %v, want %v", tc.contentType, tc.filename, got, tc.want)
			}
		})
	}
}

// --- normalizeQQURL ---

func TestNormalizeQQURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"//example.com/path", "https://example.com/path"},
		{"https://example.com/path", "https://example.com/path"},
		{"  https://example.com/path  ", "https://example.com/path"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeQQURL(tc.input)
			if got != tc.want {
				t.Errorf("normalizeQQURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// --- isQQTokenExpiredBody (more branches) ---

func TestIsQQTokenExpiredBody(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"ok_status", 200, "", false},
		{"code_11244", 400, `{"code":11244}`, true},
		{"err_code_11244", 400, `{"err_code":11244}`, true},
		{"token_expired_msg", 401, `token not exist or expire`, true},
		{"other_error", 400, `{"code":99999}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isQQTokenExpiredBody(tc.status, []byte(tc.body))
			if got != tc.want {
				t.Errorf("isQQTokenExpiredBody() = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- resolveQQSTTConfig ---

func TestResolveQQSTTConfig_Empty(t *testing.T) {
	cfg := resolveQQSTTConfig(config.IMSTTConfig{}, nil)
	if cfg != nil {
		t.Error("empty config should return nil")
	}
}

func TestResolveQQSTTConfig_FromGlobal(t *testing.T) {
	cfg := resolveQQSTTConfig(config.IMSTTConfig{
		BaseURL: "https://api.example.com",
		APIKey:  "key123",
		Model:   "whisper-1",
	}, nil)
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.BaseURL != "https://api.example.com" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestResolveQQSTTConfig_ExtraOverride(t *testing.T) {
	cfg := resolveQQSTTConfig(config.IMSTTConfig{
		BaseURL: "https://global.example.com",
		APIKey:  "global-key",
		Model:   "global-model",
	}, map[string]any{
		"stt": map[string]any{
			"baseUrl": "https://override.example.com",
			"apiKey":  "override-key",
			"model":   "override-model",
		},
	})
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.BaseURL != "https://override.example.com" {
		t.Errorf("BaseURL = %q, want override", cfg.BaseURL)
	}
}

// --- isQQMarkdownRejected ---

func TestIsQQMarkdownRejected(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not_match", fmt.Errorf("QQ API [400] /messages: bad request"), false},
		{"match", fmt.Errorf("QQ API [500] /messages: invalid request {\"code\":11255}"), true},
		{"partial_500", fmt.Errorf("QQ API [500] other"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isQQMarkdownRejected(tc.err)
			if got != tc.want {
				t.Errorf("isQQMarkdownRejected() = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- parseQQExpiresIn ---

func TestParseQQExpiresIn(t *testing.T) {
	tests := []struct {
		input any
		want  int
		ok    bool
	}{
		{"7200", 7200, true},
		{"", 0, true},
		{"abc", 0, false},
		{nil, 0, true}, // stringFromAny(nil) = "" → empty case
	}
	for i, tc := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			got, err := parseQQExpiresIn(tc.input)
			if (err != nil) == tc.ok && tc.want == 0 && err != nil {
				// error case
				return
			}
			if got != tc.want {
				t.Errorf("parseQQExpiresIn(%v) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// --- qqEnvelopeFields ---

func TestQQEnvelopeFields(t *testing.T) {
	tests := []struct {
		eventType string
		payload   map[string]any
		wantType  string
	}{
		{
			"C2C_MESSAGE_CREATE",
			map[string]any{"author": map[string]any{"user_openid": "u1", "username": "alice"}},
			"c2c",
		},
		{
			"GROUP_AT_MESSAGE_CREATE",
			map[string]any{"group_openid": "g1", "author": map[string]any{"member_openid": "m1", "username": "bob"}},
			"group",
		},
		{
			"DIRECT_MESSAGE_CREATE",
			map[string]any{"guild_id": "guild1", "author": map[string]any{"id": "a1", "username": "carol"}},
			"dm",
		},
		{
			"GUILD_MESSAGE_CREATE",
			map[string]any{"channel_id": "ch1", "author": map[string]any{"id": "a2", "username": "dave"}, "member": map[string]any{"nick": "DaveN"}},
			"guild",
		},
	}
	for _, tc := range tests {
		t.Run(tc.eventType, func(t *testing.T) {
			_, _, _, chatType := qqEnvelopeFields(tc.eventType, tc.payload)
			if chatType != tc.wantType {
				t.Errorf("chatType = %q, want %q", chatType, tc.wantType)
			}
		})
	}
}
