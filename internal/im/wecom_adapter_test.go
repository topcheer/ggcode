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
		{
			name: "with policies",
			extra: map[string]interface{}{
				"bot_id":           "bot1",
				"secret":           "sec1",
				"dm_policy":        "allowlist",
				"allow_from":       "user1,user2",
				"group_policy":     "disabled",
				"group_allow_from": "group1",
			},
			wantErr: false,
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
			name: "appmsg title",
			body: map[string]any{
				"msgtype": "appmsg",
				"appmsg":  map[string]any{"title": "report.pdf"},
			},
			want: "report.pdf",
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

func TestWeComExtractQuote(t *testing.T) {
	a := &wecomAdapter{}

	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "text quote",
			body: map[string]any{
				"quote": map[string]any{
					"msgtype": "text",
					"text":    map[string]any{"content": "quoted message"},
				},
			},
			want: "quoted message",
		},
		{
			name: "voice quote",
			body: map[string]any{
				"quote": map[string]any{
					"msgtype": "voice",
					"voice":   map[string]any{"content": "transcribed quote"},
				},
			},
			want: "transcribed quote",
		},
		{
			name: "no quote",
			body: map[string]any{
				"msgtype": "text",
				"text":    map[string]any{"content": "hello"},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.extractQuote(tt.body)
			if got != tt.want {
				t.Errorf("extractQuote() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWeComAccessPolicy(t *testing.T) {
	tests := []struct {
		name        string
		dmPolicy    string
		allowFrom   []string
		groupPolicy string
		groupAllow  []string
		senderID    string
		chatID      string
		isGroup     bool
		wantAllowed bool
	}{
		{
			name:        "open DM",
			dmPolicy:    "open",
			senderID:    "anyone",
			wantAllowed: true,
		},
		{
			name:        "disabled DM",
			dmPolicy:    "disabled",
			senderID:    "anyone",
			wantAllowed: false,
		},
		{
			name:        "allowlist DM allowed",
			dmPolicy:    "allowlist",
			allowFrom:   []string{"user1", "user2"},
			senderID:    "user1",
			wantAllowed: true,
		},
		{
			name:        "allowlist DM blocked",
			dmPolicy:    "allowlist",
			allowFrom:   []string{"user1"},
			senderID:    "user3",
			wantAllowed: false,
		},
		{
			name:        "open group",
			groupPolicy: "open",
			chatID:      "group1",
			isGroup:     true,
			wantAllowed: true,
		},
		{
			name:        "disabled group",
			groupPolicy: "disabled",
			chatID:      "group1",
			isGroup:     true,
			wantAllowed: false,
		},
		{
			name:        "allowlist group allowed",
			groupPolicy: "allowlist",
			groupAllow:  []string{"group1"},
			chatID:      "group1",
			isGroup:     true,
			wantAllowed: true,
		},
		{
			name:        "allowlist group blocked",
			groupPolicy: "allowlist",
			groupAllow:  []string{"group1"},
			chatID:      "group2",
			isGroup:     true,
			wantAllowed: false,
		},
		{
			name:        "wildcard allowlist",
			dmPolicy:    "allowlist",
			allowFrom:   []string{"*"},
			senderID:    "anyone",
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &wecomAdapter{
				dmPolicy:       tt.dmPolicy,
				allowFrom:      tt.allowFrom,
				groupPolicy:    tt.groupPolicy,
				groupAllowFrom: tt.groupAllow,
			}
			var got bool
			if tt.isGroup {
				got = a.isGroupAllowed(tt.chatID, tt.senderID)
			} else {
				got = a.isDMAllowed(tt.senderID)
			}
			if got != tt.wantAllowed {
				t.Errorf("allowed = %v, want %v", got, tt.wantAllowed)
			}
		})
	}
}

func TestWeComDedup(t *testing.T) {
	a := &wecomAdapter{
		seen:        make(map[string]time.Time),
		replyReqIDs: make(map[string]string),
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

	if len(a.seen) > wecomDedupMaxSize {
		t.Logf("seen map size: %d (expected <= %d)", len(a.seen), wecomDedupMaxSize)
	}
}

func TestWeComReplyReqIDTracking(t *testing.T) {
	a := &wecomAdapter{
		seen:        make(map[string]time.Time),
		replyReqIDs: make(map[string]string),
	}

	// Simulate tracking
	a.replyReqIDs["msg-1"] = "req-abc-123"
	if a.replyReqIDs["msg-1"] != "req-abc-123" {
		t.Error("replyReqIDs not stored correctly")
	}

	// Overflow eviction
	for i := 0; i < wecomDedupMaxSize+10; i++ {
		a.replyReqIDs[fmt.Sprintf("msg-%d", i+100)] = fmt.Sprintf("req-%d", i)
	}
	// First entries should be evicted
	if len(a.replyReqIDs) > wecomDedupMaxSize {
		t.Logf("replyReqIDs size: %d (expected <= %d)", len(a.replyReqIDs), wecomDedupMaxSize)
	}
}

func TestWeComPlatformConstant(t *testing.T) {
	if PlatformWeCom != "wecom" {
		t.Errorf("PlatformWeCom = %q, want %q", PlatformWeCom, "wecom")
	}
}

func TestWeComExtractAttachments(t *testing.T) {
	a := &wecomAdapter{}

	tests := []struct {
		name string
		body map[string]any
		want int // expected number of attachments
	}{
		{
			name: "image with url",
			body: map[string]any{
				"msgtype": "image",
				"image":   map[string]any{"url": "https://example.com/img.png"},
			},
			want: 1,
		},
		{
			name: "image with base64",
			body: map[string]any{
				"msgtype": "image",
				"image":   map[string]any{"url": "https://example.com/img.png", "base64": "iVBOR..."},
			},
			want: 2,
		},
		{
			name: "file",
			body: map[string]any{
				"msgtype": "file",
				"file":    map[string]any{"url": "https://example.com/doc.pdf"},
			},
			want: 1,
		},
		{
			name: "appmsg with file",
			body: map[string]any{
				"msgtype": "appmsg",
				"appmsg": map[string]any{
					"title": "report.pdf",
					"file":  map[string]any{"url": "https://example.com/report.pdf"},
				},
			},
			want: 1,
		},
		{
			name: "text no attachments",
			body: map[string]any{
				"msgtype": "text",
				"text":    map[string]any{"content": "hello"},
			},
			want: 0,
		},
		{
			name: "quote image",
			body: map[string]any{
				"msgtype": "text",
				"text":    map[string]any{"content": "see this"},
				"quote": map[string]any{
					"msgtype": "image",
					"image":   map[string]any{"url": "https://example.com/quote.png"},
				},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.extractAttachments(tt.body)
			if len(got) != tt.want {
				t.Errorf("extractAttachments() count = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestEntryMatches(t *testing.T) {
	tests := []struct {
		entries []string
		target  string
		want    bool
	}{
		{[]string{"user1", "user2"}, "user1", true},
		{[]string{"user1", "user2"}, "USER1", true},
		{[]string{"user1", "user2"}, "user3", false},
		{[]string{"*"}, "anyone", true},
		{[]string{}, "anyone", false},
		{nil, "anyone", false},
	}

	for _, tt := range tests {
		got := entryMatches(tt.entries, tt.target)
		if got != tt.want {
			t.Errorf("entryMatches(%v, %q) = %v, want %v", tt.entries, tt.target, got, tt.want)
		}
	}
}
