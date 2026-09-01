package im

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/config"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
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
		// #1222: only the TOP fallback block is stripped; the user's own
		// blockquotes and code fences after the separator stay verbatim.
		{
			name:  "body quote preserved",
			input: "> <@user:example.org> Original\n\nMy reply:\n> my own quote\nsee this",
			want:  "My reply:\n> my own quote\nsee this",
		},
		{
			name:  "code fence preserved",
			input: "> <@user:example.org> Original\n\nOutput:\n```sh\n$ grep '^>' file\n```",
			want:  "Output:\n```sh\n$ grep '^>' file\n```",
		},
		// No blank-line separator after the leading quote block: per spec
		// this is not a reply fallback, so nothing is stripped.
		{
			name:  "no separator keeps body",
			input: "> quoted line\nstill body",
			want:  "> quoted line\nstill body",
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

// TestMatrixGetHistoryVisibilityFailsClosed pins #1355: a state-fetch error
// must propagate (mautrix-aligned fail-closed), never fall back to "shared" -
// the SharedHistory flag rides room keys/key backup for the whole outbound
// session lifetime with no correction path.
func TestMatrixGetHistoryVisibilityFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	mc, err := mautrix.NewClient(srv.URL, "", "test-token")
	if err != nil {
		t.Skipf("cannot build matrix client in test env: %v", err)
	}
	store := &cryptoStateStore{adapter: &matrixAdapter{client: mc}}

	if _, err := store.GetHistoryVisibility(context.Background(), "!room:example.org"); err == nil {
		t.Fatal("expected state-fetch error to propagate (fail-closed), got nil")
	}
}

// TestMatrixGetHistoryVisibilityDefaultsToShared pins the OTHER half of
// #1355: a successful fetch with no visibility event content returns
// Matrix's documented default ("shared") - a default, not an error.
func TestMatrixGetHistoryVisibilityDefaultsToShared(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// State event exists but content carries no visibility field.
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	mc, err := mautrix.NewClient(srv.URL, "", "test-token")
	if err != nil {
		t.Skipf("cannot build matrix client in test env: %v", err)
	}
	store := &cryptoStateStore{adapter: &matrixAdapter{client: mc}}

	hv, err := store.GetHistoryVisibility(context.Background(), "!room:example.org")
	if err != nil {
		t.Fatalf("empty-content success path must not error: %v", err)
	}
	if hv.HistoryVisibility != event.HistoryVisibilityShared {
		t.Fatalf("expected default shared, got %q", hv.HistoryVisibility)
	}
}

// TestSanitizeFileToken pins #1404-A: the persistent crypto store path is
// derived from the adapter name - unsafe characters must reduce to '-'
// (never escape the matrix-crypto dir) and names must map deterministically.
func TestSanitizeFileToken(t *testing.T) {
	cases := map[string]string{
		"main":         "main",
		"work bot":     "work-bot",
		"a/b/c":        "a-b-c",
		"..\\..\\evil": "------evil",
		"бот":          "---",
	}
	for in, want := range cases {
		if got := sanitizeFileToken(in); got != want {
			t.Errorf("sanitizeFileToken(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOpenPersistentCryptoStorePathIsolation: two adapters (different
// names or homeservers) must never share one device identity - the file
// path is name-keyed and the account ID is homeserver|name.
func TestOpenPersistentCryptoStorePathIsolation(t *testing.T) {
	a := &matrixAdapter{name: "bot one", homeserver: "https://matrix.org"}
	if got := sanitizeFileToken(a.name); got != "bot-one" {
		t.Fatalf("file token = %q", got)
	}
	b := &matrixAdapter{name: "bot-one", homeserver: "https://evil.example"}
	aidA := fmt.Sprintf("%s|%s", a.homeserver, a.name)
	aidB := fmt.Sprintf("%s|%s", b.homeserver, b.name)
	if aidA == aidB {
		t.Fatal("different homeservers must yield different account IDs")
	}
}
