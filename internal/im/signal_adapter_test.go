package im

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewSignalAdapter_MissingAccount(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"base_url": "http://127.0.0.1:8080",
		},
	}
	_, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err == nil {
		t.Fatal("expected error for missing account")
	}
	if !strings.Contains(err.Error(), "account") {
		t.Errorf("error should mention account: %v", err)
	}
}

func TestNewSignalAdapter_ValidConfig(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"base_url": "http://127.0.0.1:8080",
			"account":  "+1234567890",
		},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.account != "+1234567890" {
		t.Errorf("account = %q, want %q", a.account, "+1234567890")
	}
	if a.baseURL != "http://127.0.0.1:8080" {
		t.Errorf("baseURL = %q, want %q", a.baseURL, "http://127.0.0.1:8080")
	}
}

func TestNewSignalAdapter_DefaultBaseURL(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.baseURL != "http://127.0.0.1:8080" {
		t.Errorf("baseURL = %q, want default %q", a.baseURL, "http://127.0.0.1:8080")
	}
}

func TestNewSignalAdapter_BaseURLNoProtocol(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"base_url": "192.168.1.100:8080",
			"account":  "+1234567890",
		},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.baseURL != "http://192.168.1.100:8080" {
		t.Errorf("baseURL = %q, want %q", a.baseURL, "http://192.168.1.100:8080")
	}
}

func TestNewSignalAdapter_RequireMention(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account":         "+1234567890",
			"require_mention": "false",
		},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.requireMention {
		t.Error("requireMention should be false when set to 'false'")
	}
}

func TestNewSignalAdapter_GroupAllowlist(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account":         "+1234567890",
			"group_allowlist": "abc123,def456",
		},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.groupAllowlist) != 2 {
		t.Fatalf("groupAllowlist len = %d, want 2", len(a.groupAllowlist))
	}
	if a.groupAllowlist[0] != "abc123" {
		t.Errorf("groupAllowlist[0] = %q, want %q", a.groupAllowlist[0], "abc123")
	}
}

func TestStripSignalMention(t *testing.T) {
	tests := []struct {
		text    string
		account string
		want    string
	}{
		{
			text:    "+1234567890 hello world",
			account: "+1234567890",
			want:    "hello world",
		},
		{
			text:    "hey +1234567890 what's up",
			account: "+1234567890",
			want:    "hey what's up",
		},
		{
			text:    "hello world",
			account: "+1234567890",
			want:    "hello world",
		},
		{
			text:    "1234567890 hello",
			account: "+1234567890",
			want:    "hello",
		},
	}
	for _, tt := range tests {
		got := stripSignalMention(tt.text, tt.account)
		if got != tt.want {
			t.Errorf("stripSignalMention(%q, %q) = %q, want %q", tt.text, tt.account, got, tt.want)
		}
	}
}

func TestRenderSignalMentions(t *testing.T) {
	text := "hello \ufffc how are you"
	mentions := []any{
		map[string]any{
			"name":   "Alice",
			"number": "+1111111111",
		},
	}
	got := renderSignalMentions(text, mentions)
	want := "hello @Alice how are you"
	if got != want {
		t.Errorf("renderSignalMentions() = %q, want %q", got, want)
	}
}

func TestSplitSignalMessage(t *testing.T) {
	// Short message
	chunks := splitSignalMessage("hello", 10)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("short message: %v", chunks)
	}

	// Long message, split at newline
	long := "line1\nline2\nline3\nline4\nline5"
	chunks = splitSignalMessage(long, 12)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
	// Verify reassembly
	rejoined := strings.Join(chunks, "")
	if rejoined != long {
		t.Errorf("rejoined = %q, want %q", rejoined, long)
	}
}

func TestSplitSignalMessage_DoesNotBreakUTF8(t *testing.T) {
	msg := "你好世界🙂再见"
	chunks := splitSignalMessage(msg, 3)
	if got := strings.Join(chunks, ""); got != msg {
		t.Fatalf("rejoined = %q, want %q", got, msg)
	}
	for i, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %d invalid UTF-8: %q", i, chunk)
		}
		if len([]rune(chunk)) > 3 {
			t.Fatalf("chunk %d has %d runes, want <= 3", i, len([]rune(chunk)))
		}
	}
}

func TestJsonInt64(t *testing.T) {
	tests := []struct {
		m    map[string]any
		key  string
		want int64
	}{
		{map[string]any{"ts": float64(1234567890)}, "ts", 1234567890},
		{map[string]any{"ts": "1234567890"}, "ts", 1234567890},
		{map[string]any{"ts": int64(123)}, "ts", 123},
		{map[string]any{}, "missing", 0},
	}
	for _, tt := range tests {
		got := jsonInt64(tt.m, tt.key)
		if got != tt.want {
			t.Errorf("jsonInt64(%v, %q) = %d, want %d", tt.m, tt.key, got, tt.want)
		}
	}
}

func TestNewSignalAdapter_AllowedUsers(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account":       "+1234567890",
			"allowed_users": "+1111111111,+2222222222",
		},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.allowedUsers) != 2 {
		t.Fatalf("allowedUsers len = %d, want 2", len(a.allowedUsers))
	}
}

func TestNewSignalAdapter_AdapterName(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, err := newSignalAdapter("my-signal", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "my-signal" {
		t.Errorf("Name() = %q, want %q", a.Name(), "my-signal")
	}
}

func TestNewSignalAdapter_TriggerTyping(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	// TriggerTyping should work (won't error without connection, just fails silently)
	_ = a.TriggerTyping(nil, ChannelBinding{ChannelID: "+0987654321"})
}

func TestNewSignalAdapter_EnvFallback(t *testing.T) {
	os.Setenv("SIGNAL_ACCOUNT", "+9999999999")
	defer os.Unsetenv("SIGNAL_ACCOUNT")

	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra:    map[string]interface{}{},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.account != "+9999999999" {
		t.Errorf("account = %q, want %q from env", a.account, "+9999999999")
	}
}

func TestSignalEchoSuppression(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	// Add sent timestamp
	a.addSentTimestamp(1000)
	if !a.isSentTimestamp(1000) {
		t.Error("should find sent timestamp 1000")
	}
	if a.isSentTimestamp(2000) {
		t.Error("should not find unsent timestamp 2000")
	}

	// Remove it
	a.removeSentTimestamp(1000)
	if a.isSentTimestamp(1000) {
		t.Error("should not find removed timestamp")
	}

	// Max limit
	for i := int64(0); i < signalMaxSentTimestamps+10; i++ {
		a.addSentTimestamp(i)
	}
	if len(a.sentTimestamps) > signalMaxSentTimestamps {
		t.Errorf("should cap at %d, got %d", signalMaxSentTimestamps, len(a.sentTimestamps))
	}
}

func TestSignalProcessEnvelope_SelfMessage(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	// Self-message should be ignored (no syncMessage, sourceNumber == account)
	envelope := map[string]any{
		"sourceNumber": "+1234567890",
		"dataMessage": map[string]any{
			"message":   "hello from myself",
			"timestamp": float64(1000),
		},
	}
	// processEnvelope won't call HandleInbound without a manager, so just verify it doesn't panic
	a.processEnvelope(nil, envelope)
}

func TestSignalProcessEnvelope_EmptyDataMessage(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	// No dataMessage — should be ignored
	envelope := map[string]any{
		"sourceNumber": "+1111111111",
	}
	a.processEnvelope(nil, envelope)

	// Empty text
	envelope = map[string]any{
		"sourceNumber": "+1111111111",
		"dataMessage": map[string]any{
			"message":   "",
			"timestamp": float64(1001),
		},
	}
	a.processEnvelope(nil, envelope)
}

func TestSignalProcessEnvelope_GroupNoAllowlist(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	// Group message with no group_allowlist — should be ignored
	envelope := map[string]any{
		"sourceNumber": "+1111111111",
		"dataMessage": map[string]any{
			"message":   "hello group",
			"timestamp": float64(1002),
			"groupInfo": map[string]any{
				"groupId": "group-abc123",
			},
		},
	}
	a.processEnvelope(nil, envelope)
}

func TestSignalProcessEnvelope_GroupWithAllowlist(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account":         "+1234567890",
			"group_allowlist": "group-abc123",
			"require_mention": "false",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	// Group message with matching allowlist
	envelope := map[string]any{
		"sourceNumber": "+1111111111",
		"dataMessage": map[string]any{
			"message":   "hello group",
			"timestamp": float64(1003),
			"groupInfo": map[string]any{
				"groupId": "group-abc123",
			},
		},
	}
	a.processEnvelope(nil, envelope)

	// Group not in allowlist
	envelope2 := map[string]any{
		"sourceNumber": "+1111111111",
		"dataMessage": map[string]any{
			"message":   "hello other group",
			"timestamp": float64(1004),
			"groupInfo": map[string]any{
				"groupId": "group-other",
			},
		},
	}
	a.processEnvelope(nil, envelope2)
}

func TestSignalProcessEnvelope_UserNotAllowed(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account":       "+1234567890",
			"allowed_users": "+1111111111",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	// User not in allowed list
	envelope := map[string]any{
		"sourceNumber": "+9999999999",
		"dataMessage": map[string]any{
			"message":   "hello",
			"timestamp": float64(1005),
		},
	}
	a.processEnvelope(nil, envelope)
}

func TestSignalDedup(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	// Same timestamp twice — second should be deduped
	envelope := map[string]any{
		"sourceNumber": "+1111111111",
		"dataMessage": map[string]any{
			"message":   "first",
			"timestamp": float64(2000),
		},
	}
	a.processEnvelope(nil, envelope)
	a.processEnvelope(nil, envelope) // should be deduped
}
