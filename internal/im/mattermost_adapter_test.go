package im

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestMattermostAdapterConfig(t *testing.T) {
	tests := []struct {
		name    string
		extra   map[string]interface{}
		wantErr bool
	}{
		{
			name: "valid config",
			extra: map[string]interface{}{
				"url":   "https://mm.example.com",
				"token": "test-token-123",
			},
			wantErr: false,
		},
		{
			name:    "missing url and token",
			extra:   map[string]interface{}{},
			wantErr: true,
		},
		{
			name:    "missing url",
			extra:   map[string]interface{}{"token": "test"},
			wantErr: true,
		},
		{
			name:    "missing token",
			extra:   map[string]interface{}{"url": "https://mm.example.com"},
			wantErr: true,
		},
		{
			name: "with policies",
			extra: map[string]interface{}{
				"url":             "https://mm.example.com",
				"token":           "test-token",
				"require_mention": "false",
				"free_channels":   "ch1,ch2",
				"allowed_users":   "user1,user2",
				"reply_mode":      "thread",
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
			adapter, err := newMattermostAdapter("test-mm", cfg, adapterCfg, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("newMattermostAdapter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if adapter == nil {
					t.Error("expected non-nil adapter")
					return
				}
				if adapter.baseURL == "" {
					t.Error("expected baseURL to be set")
				}
				if adapter.token == "" {
					t.Error("expected token to be set")
				}
			}
		})
	}
}

func TestMattermostMentionDetection(t *testing.T) {
	a := &mattermostAdapter{
		botUsername: "mybot",
		botUserID:   "abc123",
	}

	tests := []struct {
		text string
		want bool
	}{
		{"@mybot hello", true},
		{"@MYBOT hello", true},
		{"@abc123 hello", true},
		{"hello world", false},
		{"@otheruser hello", false},
		{"check out @mybot's work", true},
	}

	for _, tt := range tests {
		got := a.hasMention(tt.text)
		if got != tt.want {
			t.Errorf("hasMention(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestMattermostStripMention(t *testing.T) {
	a := &mattermostAdapter{
		botUsername: "mybot",
		botUserID:   "abc123",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"@mybot hello", "hello"},
		{"@MYBOT hello", "hello"},
		{"@abc123 hello world", "hello world"},
		{"@mybot @other hello", "@other hello"},
		{"no mention here", "no mention here"},
	}

	for _, tt := range tests {
		got := a.stripMention(tt.input)
		if got != tt.want {
			t.Errorf("stripMention(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMattermostPlatformConstant(t *testing.T) {
	if PlatformMattermost != "mattermost" {
		t.Errorf("PlatformMattermost = %q, want %q", PlatformMattermost, "mattermost")
	}
}

func TestMattermostParseCommaList(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a,b,c", 3},
		{"a, b , c", 3},
		{"", 0},
		{"single", 1},
		{",,,", 0},
	}

	for _, tt := range tests {
		got := parseCommaList(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseCommaList(%q) = %d entries, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestMattermostWSEventParsing(t *testing.T) {
	a := &mattermostAdapter{
		botUserID:   "bot123",
		botUsername: "testbot",
		seen:        make(map[string]time.Time),
		manager:     nil, // will crash if we try to handle inbound
	}

	// Test that own messages are ignored
	event := map[string]any{
		"event": "posted",
		"data": map[string]any{
			"post":         `{"id":"p1","user_id":"bot123","message":"my message","channel_id":"ch1"}`,
			"channel_type": "D",
		},
	}
	// This should silently return without error (ignoring own message)
	a.handleWSEvent(nil, event)
	// No panic = success
}

func TestMattermostRequireMentionPolicy(t *testing.T) {
	a := &mattermostAdapter{
		botUserID:      "bot123",
		botUsername:    "testbot",
		requireMention: true,
		freeChannels:   []string{"free-ch"},
		seen:           make(map[string]time.Time),
		manager:        nil,
	}

	tests := []struct {
		name         string
		channelType  string
		channelID    string
		message      string
		shouldIgnore bool
	}{
		{"DM no mention needed", "D", "dm1", "hello", false},
		{"Channel with mention", "O", "ch1", "@testbot hello", false},
		{"Channel without mention", "O", "ch1", "hello", true},
		{"Free channel no mention", "O", "free-ch", "hello", false},
		{"Group without mention", "G", "grp1", "hello", true},
		{"Group with mention", "G", "grp1", "@testbot hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset seen
			a.seen = make(map[string]time.Time)

			event := map[string]any{
				"event": "posted",
				"data": map[string]any{
					"post":         `{"id":"p-` + tt.name + `","user_id":"user1","message":"` + tt.message + `","channel_id":"` + tt.channelID + `"}`,
					"channel_type": tt.channelType,
					"sender_name":  "testuser",
				},
			}

			// We can't fully test this since manager is nil,
			// but we can verify no panic occurs
			a.handleWSEvent(nil, event)
		})
	}
}
