package im

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewMatrixAdapter_MissingHomeserver(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "matrix",
		Extra: map[string]interface{}{
			"access_token": "tok123",
		},
	}
	_, err := newMatrixAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err == nil {
		t.Fatal("expected error for missing homeserver")
	}
}

func TestNewMatrixAdapter_MissingToken(t *testing.T) {
	orig := os.Getenv("GGCODE_IM_MATRIX_ACCESS_TOKEN")
	os.Unsetenv("GGCODE_IM_MATRIX_ACCESS_TOKEN")
	os.Unsetenv("MATRIX_ACCESS_TOKEN")
	defer func() {
		if orig != "" {
			os.Setenv("GGCODE_IM_MATRIX_ACCESS_TOKEN", orig)
		}
	}()
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "matrix",
		Extra: map[string]interface{}{
			"homeserver": "https://matrix.example.org",
		},
	}
	_, err := newMatrixAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestNewMatrixAdapter_ValidConfig(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "matrix",
		Extra: map[string]interface{}{
			"homeserver":   "https://matrix.example.org",
			"access_token": "syt_xxx",
			"user_id":      "@bot:example.org",
		},
	}
	a, err := newMatrixAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.homeserver != "https://matrix.example.org" {
		t.Errorf("homeserver = %q, want %q", a.homeserver, "https://matrix.example.org")
	}
	if a.token != "syt_xxx" {
		t.Errorf("token = %q, want %q", a.token, "syt_xxx")
	}
	if a.userID != "@bot:example.org" {
		t.Errorf("userID = %q, want %q", a.userID, "@bot:example.org")
	}
}

func TestNewMatrixAdapter_RequireMention(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "matrix",
		Extra: map[string]interface{}{
			"homeserver":      "https://matrix.example.org",
			"access_token":    "tok",
			"require_mention": "false",
		},
	}
	a, err := newMatrixAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.requireMention {
		t.Error("requireMention should be false when set to 'false'")
	}
}

func TestNewMatrixAdapter_FreeRooms(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "matrix",
		Extra: map[string]interface{}{
			"homeserver":   "https://matrix.example.org",
			"access_token": "tok",
			"free_rooms":   "!room1:example.org,!room2:example.org",
		},
	}
	a, err := newMatrixAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.freeRooms) != 2 {
		t.Fatalf("freeRooms = %v, want 2 entries", a.freeRooms)
	}
	if a.freeRooms[0] != "!room1:example.org" {
		t.Errorf("freeRooms[0] = %q", a.freeRooms[0])
	}
}

func TestStripMatrixReplyFallback(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fallback",
			input: "Hello world",
			want:  "Hello world",
		},
		{
			name:  "simple reply fallback",
			input: "> <@user:example.org> Original message\n\nMy reply",
			want:  "My reply",
		},
		{
			name:  "multiline fallback",
			input: "> <@user:example.org> Line 1\n> Line 2\n\nMy reply here",
			want:  "My reply here",
		},
		{
			name:  "only fallback no body",
			input: "> <@user:example.org> Original",
			want:  "> <@user:example.org> Original",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMatrixReplyFallback(tt.input)
			if got != tt.want {
				t.Errorf("stripMatrixReplyFallback(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMatrixAdapter_HasMention(t *testing.T) {
	a := &matrixAdapter{userID: "@bot:example.org"}
	tests := []struct {
		name    string
		body    string
		content map[string]any
		want    bool
	}{
		{
			name:    "full user ID in body",
			body:    "@bot:example.org hello",
			content: map[string]any{},
			want:    true,
		},
		{
			name:    "local part in body",
			body:    "hey bot can you help",
			content: map[string]any{},
			want:    true,
		},
		{
			name:    "no mention",
			body:    "hello world",
			content: map[string]any{},
			want:    false,
		},
		{
			name: "m.mentions user_ids match",
			body: "check this out",
			content: map[string]any{
				"m.mentions": map[string]any{
					"user_ids": []any{"@bot:example.org"},
				},
			},
			want: true,
		},
		{
			name: "m.mentions user_ids no match",
			body: "check this out",
			content: map[string]any{
				"m.mentions": map[string]any{
					"user_ids": []any{"@other:example.org"},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.hasMention(tt.body, tt.content)
			if got != tt.want {
				t.Errorf("hasMention(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestMatrixAdapter_StripMention(t *testing.T) {
	a := &matrixAdapter{userID: "@bot:example.org"}
	tests := []struct {
		input string
		want  string
	}{
		{"@bot:example.org hello", "hello"},
		{"@bot check this", "check this"},
		{"hello world", "hello world"},
	}
	for _, tt := range tests {
		got := a.stripMention(tt.input)
		if got != tt.want {
			t.Errorf("stripMention(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitMatrixMessage(t *testing.T) {
	// Short message
	chunks := chunkText("hello", 10)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("short message: %v", chunks)
	}

	// Long message, no newlines
	long := strings.Repeat("a", 100)
	chunks = chunkText(long, 30)
	if len(chunks) < 3 {
		t.Errorf("expected >= 3 chunks, got %d", len(chunks))
	}

	// Long message with newlines
	longWithNewlines := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10"
	chunks = chunkText(longWithNewlines, 20)
	for _, chunk := range chunks {
		if len(chunk) > 25 { // allow slightly over for newline splits
			t.Errorf("chunk too long (%d): %q", len(chunk), chunk)
		}
	}
}

func TestSplitMatrixMessage_DoesNotBreakUTF8(t *testing.T) {
	msg := "你好世界🙂再见"
	chunks := chunkText(msg, 3)
	if strings.Join(chunks, "") != msg {
		t.Fatalf("reassembled = %q, want %q", strings.Join(chunks, ""), msg)
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

func TestMatrixAdapter_DMRoomDetection(t *testing.T) {
	a := &matrixAdapter{dmRooms: map[string]bool{
		"!dm1:example.org": true,
		"!dm2:example.org": true,
	}}
	if !a.dmRooms["!dm1:example.org"] {
		t.Error("dm room !dm1 should be detected")
	}
	if a.dmRooms["!group:example.org"] {
		t.Error("group room should not be DM")
	}
}
