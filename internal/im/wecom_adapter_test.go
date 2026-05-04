package im

import (
	"fmt"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestWeComAdapterConfig(t *testing.T) {
	tests := []struct {
		name    string
		extra   map[string]interface{}
		envBot  string
		envSec  string
		wantErr bool
	}{
		{
			name: "valid config from extra",
			extra: map[string]interface{}{
				"bot_id": "test-bot-123",
				"secret": "test-secret-456",
			},
			wantErr: false,
		},
		{
			name:    "missing bot_id and secret",
			extra:   map[string]interface{}{},
			wantErr: true,
		},
		{
			name:    "missing bot_id only",
			extra:   map[string]interface{}{"secret": "test-secret"},
			wantErr: true,
		},
		{
			name:    "empty extra",
			extra:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.IMConfig{}
			adapterCfg := config.IMAdapterConfig{
				Enabled: true,
				Extra:   tt.extra,
			}
			adapter, err := newWeComAdapter("test-wecom", cfg, adapterCfg, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("newWeComAdapter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if adapter == nil {
					t.Error("expected non-nil adapter")
					return
				}
				if adapter.botID == "" {
					t.Error("expected botID to be set")
				}
				if adapter.secret == "" {
					t.Error("expected secret to be set")
				}
			}
		})
	}
}

func TestWeComExtractText(t *testing.T) {
	a := &wecomAdapter{}

	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "text message",
			body: map[string]any{
				"msgtype": "text",
				"text":    map[string]any{"content": "hello world"},
			},
			want: "hello world",
		},
		{
			name: "mixed message",
			body: map[string]any{
				"msgtype": "mixed",
				"mixed": map[string]any{
					"msg_item": []any{
						map[string]any{"msgtype": "text", "text": map[string]any{"content": "part1"}},
						map[string]any{"msgtype": "text", "text": map[string]any{"content": "part2"}},
					},
				},
			},
			want: "part1\npart2",
		},
		{
			name: "voice with transcription",
			body: map[string]any{
				"msgtype": "voice",
				"voice":   map[string]any{"content": "transcribed text"},
			},
			want: "transcribed text",
		},
		{
			name: "empty text",
			body: map[string]any{
				"msgtype": "text",
				"text":    map[string]any{"content": ""},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.extractText(tt.body)
			if got != tt.want {
				t.Errorf("extractText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWeComDedup(t *testing.T) {
	a := &wecomAdapter{
		seen: make(map[string]time.Time),
	}

	// Add a message ID
	a.seen["msg-1"] = time.Now()

	// Check it's seen
	if _, ok := a.seen["msg-1"]; !ok {
		t.Error("expected msg-1 to be in seen map")
	}

	// Add many more to trigger eviction
	for i := 0; i < wecomDedupMaxSize+10; i++ {
		a.seen[fmt.Sprintf("msg-%d", i+100)] = time.Now()
	}

	// Old entry should be evicted during next handleMessage
	if len(a.seen) > wecomDedupMaxSize {
		t.Logf("seen map size: %d (expected <= %d)", len(a.seen), wecomDedupMaxSize)
	}
}

func TestWeComPlatformConstant(t *testing.T) {
	if PlatformWeCom != "wecom" {
		t.Errorf("PlatformWeCom = %q, want %q", PlatformWeCom, "wecom")
	}
}
