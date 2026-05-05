package im

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewTwitchAdapter_MissingToken(t *testing.T) {
	_, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch", Extra: map[string]interface{}{},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("expected token error: %v", err)
	}
}

func TestNewTwitchAdapter_MissingNick(t *testing.T) {
	_, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch", Extra: map[string]interface{}{"token": "oauth:xxx"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "nick") {
		t.Errorf("expected nick error: %v", err)
	}
}

func TestNewTwitchAdapter_ValidConfig(t *testing.T) {
	a, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{
			"token":    "oauth:abc123",
			"nick":     "MyBot",
			"channels": "channel1,channel2",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.nick != "mybot" {
		t.Errorf("nick = %q, want %q", a.nick, "mybot")
	}
	if a.token != "oauth:abc123" {
		t.Errorf("token = %q", a.token)
	}
	if len(a.channels) != 2 {
		t.Fatalf("channels len = %d, want 2", len(a.channels))
	}
	if a.channels[0] != "#channel1" {
		t.Errorf("channels[0] = %q, want #channel1", a.channels[0])
	}
}

func TestNewTwitchAdapter_TokenPrefix(t *testing.T) {
	a, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{
			"token": "abc123",
			"nick":  "bot",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(a.token, "oauth:") {
		t.Errorf("token should have oauth: prefix, got %q", a.token)
	}
}

func TestParseTwitchTags(t *testing.T) {
	tagStr := "display-name=TestUser;user-id=12345;custom-key=hello\\sworld"
	tags := parseTwitchTags(tagStr)
	if tags["display-name"] != "TestUser" {
		t.Errorf("display-name = %q", tags["display-name"])
	}
	if tags["user-id"] != "12345" {
		t.Errorf("user-id = %q", tags["user-id"])
	}
	if tags["custom-key"] != "hello world" {
		t.Errorf("custom-key = %q, want 'hello world'", tags["custom-key"])
	}
}

func TestStripTwitchMention(t *testing.T) {
	tests := []struct {
		text, nick, want string
	}{
		{"@bot hello", "bot", "hello"},
		{"bot hello world", "bot", "hello world"},
		{"hello world", "bot", "hello world"},
	}
	for _, tt := range tests {
		got := stripTwitchMention(tt.text, tt.nick)
		if got != tt.want {
			t.Errorf("stripTwitchMention(%q, %q) = %q, want %q", tt.text, tt.nick, got, tt.want)
		}
	}
}
