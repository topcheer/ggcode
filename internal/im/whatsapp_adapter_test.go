package im

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestChunkWARunes(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		count  int
	}{
		{"short", "hello", 10, 1},
		{"exact", "hello", 5, 1},
		{"split_no_newline", "hello world", 5, 3},
		{"newline boundary", "hello\nworld", 8, 2},
		{"empty", "", 10, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkWARunes(tt.input, tt.maxLen)
			if len(chunks) != tt.count {
				t.Errorf("got %d chunks, want %d: %v", len(chunks), tt.count, chunks)
			}
			// Verify each chunk fits in runes
			for i, c := range chunks {
				if len([]rune(c)) > tt.maxLen {
					t.Errorf("chunk %d exceeds maxLen: %d runes > %d", i, len([]rune(c)), tt.maxLen)
				}
			}
			// Verify reassembly (newline at split boundary)
			if tt.name != "newline boundary" && tt.input != "" {
				got := strings.Join(chunks, "")
				if got != tt.input {
					t.Errorf("reassembly mismatch:\ngot:  %q\nwant: %q", got, tt.input)
				}
			}
		})
	}
}

func TestChunkWARunes_MultiByte(t *testing.T) {
	// Chinese characters — each rune is 3 bytes but 1 rune
	text := strings.Repeat("你好世界", 1500) // 6000 runes, well over 4096
	chunks := chunkWARunes(text, 4096)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		runeCount := len([]rune(c))
		if runeCount > 4096 {
			t.Errorf("chunk %d: %d runes exceeds 4096", i, runeCount)
		}
	}
	// Verify reassembly preserves all characters
	got := strings.Join(chunks, "")
	if got != text {
		t.Error("reassembly mismatch for multi-byte text")
	}
}

func TestChunkWARunes_LongText(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		sb.WriteString("abcdefghij")
	}
	text := sb.String()

	chunks := chunkWARunes(text, 4096)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for 10000 char text, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len([]rune(c)) > 4096 {
			t.Errorf("chunk %d: %d runes exceeds 4096", i, len([]rune(c)))
		}
	}
	got := strings.Join(chunks, "")
	if got != text {
		t.Error("reassembly mismatch")
	}
}

func TestNewWhatsAppAdapter(t *testing.T) {
	mgr := NewManager()
	cfg := config.IMAdapterConfig{
		Extra: map[string]interface{}{
			"proxy": "http://localhost:7890",
		},
	}
	adapter, err := newWhatsAppAdapter("test-wa", config.IMConfig{}, cfg, mgr)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.name != "test-wa" {
		t.Errorf("name = %q, want %q", adapter.name, "test-wa")
	}
	if adapter.proxy != "http://localhost:7890" {
		t.Errorf("proxy = %q, want proxy set", adapter.proxy)
	}
	if adapter.manager != mgr {
		t.Error("manager not set")
	}
}

func TestWhatsAppAdapterImplementsInterfaces(t *testing.T) {
	var _ Sink = (*whatsappAdapter)(nil)
	var _ TypingIndicator = (*whatsappAdapter)(nil)
}

func TestWhatsAppStartReturnsImmediately(t *testing.T) {
	mgr := NewManager()
	adapter, err := newWhatsAppAdapter("test-wa", config.IMConfig{}, config.IMAdapterConfig{}, mgr)
	if err != nil {
		t.Fatal(err)
	}
	// Force connectAndServe to fail quickly in the background so the test does
	// not depend on network availability.
	blockingPath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blockingPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter.storeDir = blockingPath

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		adapter.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Start() blocked; adapter startup should return immediately and run in background")
	}
}
