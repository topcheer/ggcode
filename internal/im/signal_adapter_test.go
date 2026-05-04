package im

import (
	"strings"
	"testing"

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

func TestParseSSELine(t *testing.T) {
	tests := []struct {
		line      string
		wantField string
		wantValue string
	}{
		{"event: message", "event", "message"},
		{"data: {\"key\": \"val\"}", "data", "{\"key\": \"val\"}"},
		{"id: 12345", "id", "12345"},
		{": comment", "", ""}, // comment — field is empty
		{"nocolon", "nocolon", ""},
	}
	for _, tt := range tests {
		field, value := parseSSELine(tt.line)
		// Comments start with ":" so field will be empty
		if tt.line == ": comment" {
			if field != "" {
				t.Errorf("comment field should be empty, got %q", field)
			}
			continue
		}
		if field != tt.wantField || value != tt.wantValue {
			t.Errorf("parseSSELine(%q) = (%q, %q), want (%q, %q)", tt.line, field, value, tt.wantField, tt.wantValue)
		}
	}
}
