package im

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestChunkWAText(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		count  int
	}{
		{"short", "hello", 10, 1},
		{"exact", "hello", 5, 1},
		{"split", "hello world", 5, 3},
		{"newline boundary", "hello\nworld", 8, 2},
		{"empty", "", 10, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkWAText(tt.input, tt.maxLen)
			if len(chunks) != tt.count {
				t.Errorf("got %d chunks, want %d: %v", len(chunks), tt.count, chunks)
			}
			// Verify each chunk fits
			for i, c := range chunks {
				if len(c) > tt.maxLen {
					t.Errorf("chunk %d exceeds maxLen: %d > %d", i, len(c), tt.maxLen)
				}
			}
			// Verify reassembly (newline at split boundary is trimmed)
			if tt.name != "newline boundary" && tt.input != "" {
				got := strings.Join(chunks, "")
				if got != tt.input {
					t.Errorf("reassembly mismatch:\ngot:  %q\nwant: %q", got, tt.input)
				}
			}
		})
	}
}

func TestChunkWAText_LongText(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		sb.WriteString("abcdefghij")
	}
	text := sb.String()

	chunks := chunkWAText(text, 4096)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for 10000 char text, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 4096 {
			t.Errorf("chunk %d: len=%d exceeds 4096", i, len(c))
		}
	}
	// Verify reassembly
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
	// Compile-time check: whatsappAdapter must implement Sink
	var _ Sink = (*whatsappAdapter)(nil)
	// Compile-time check: optional TypingIndicator
	var _ TypingIndicator = (*whatsappAdapter)(nil)
}
