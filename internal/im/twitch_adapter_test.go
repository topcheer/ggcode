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
		t.Errorf("nick = %q, want %q (should lowercase)", a.nick, "mybot")
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
	if a.channels[1] != "#channel2" {
		t.Errorf("channels[1] = %q, want #channel2", a.channels[1])
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

func TestNewTwitchAdapter_ChannelHashPrefix(t *testing.T) {
	a, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{
			"token":    "oauth:xxx",
			"nick":     "bot",
			"channels": "#already,needsHash",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.channels[0] != "#already" {
		t.Errorf("channels[0] = %q, should keep #", a.channels[0])
	}
	if a.channels[1] != "#needsHash" {
		t.Errorf("channels[1] = %q, should add #", a.channels[1])
	}
}

func TestNewTwitchAdapter_AdapterName(t *testing.T) {
	a, err := newTwitchAdapter("my-twitch", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{"token": "oauth:xxx", "nick": "bot"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "my-twitch" {
		t.Errorf("Name() = %q, want %q", a.Name(), "my-twitch")
	}
}

func TestNewTwitchAdapter_TriggerTypingNoop(t *testing.T) {
	a, _ := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{"token": "oauth:xxx", "nick": "bot"},
	}, nil)
	if err := a.TriggerTyping(nil, ChannelBinding{}); err != nil {
		t.Errorf("TriggerTyping should be noop, got: %v", err)
	}
}

func TestParseTwitchTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "standard tags",
			input: "display-name=TestUser;user-id=12345",
			want:  map[string]string{"display-name": "TestUser", "user-id": "12345"},
		},
		{
			name:  "escaped spaces",
			input: "display-name=Hello\\sWorld",
			want:  map[string]string{"display-name": "Hello World"},
		},
		{
			name:  "escaped newline",
			input: "msg=Hello\\nWorld",
			want:  map[string]string{"msg": "Hello\nWorld"},
		},
		{
			name:  "escaped backslash and semicolon",
			input: "msg=path\\\\to\\\\file\\:name",
			want:  map[string]string{"msg": "path\\to\\file;name"},
		},
		{
			name:  "empty value",
			input: "key1=;key2=value",
			want:  map[string]string{"key1": "", "key2": "value"},
		},
		{
			name:  "no equals",
			input: "badtag",
			want:  map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTwitchTags(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("tags count = %d, want %d", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("tags[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestUnescapeTwitchTag(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello\\sworld", "hello world"},
		{"line1\\nline2", "line1\nline2"},
		{"path\\\\file", "path\\file"},
		{"semi\\:colon", "semi;colon"},
		{"no escapes", "no escapes"},
		{"", ""},
	}
	for _, tt := range tests {
		got := unescapeTwitchTag(tt.input)
		if got != tt.want {
			t.Errorf("unescapeTwitchTag(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripTwitchMention(t *testing.T) {
	tests := []struct {
		text, nick, want string
	}{
		{"@bot hello", "bot", "hello"},
		{"bot hello world", "bot", "hello world"},
		{"hello world", "bot", "hello world"},
		{"@Bot hello", "bot", "@Bot hello"}, // case-sensitive, @Bot not stripped when nick is lowercase
		{"@bot @bot double", "bot", "double"},
	}
	for _, tt := range tests {
		got := stripTwitchMention(tt.text, tt.nick)
		if got != tt.want {
			t.Errorf("stripTwitchMention(%q, %q) = %q, want %q", tt.text, tt.nick, got, tt.want)
		}
	}
}
