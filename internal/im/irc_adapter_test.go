package im

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewIRCAdapter_MissingNick(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra:    map[string]interface{}{},
	}
	_, err := newIRCAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err == nil {
		t.Fatal("expected error for missing nick")
	}
	if !strings.Contains(err.Error(), "nick") {
		t.Errorf("error should mention nick: %v", err)
	}
}

func TestNewIRCAdapter_ValidConfig(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra: map[string]interface{}{
			"host":     "irc.example.com",
			"port":     "6697",
			"nick":     "ggcode-bot",
			"channels": "#test,#dev",
		},
	}
	a, err := newIRCAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.host != "irc.example.com" {
		t.Errorf("host = %q, want %q", a.host, "irc.example.com")
	}
	if a.port != 6697 {
		t.Errorf("port = %d, want 6697", a.port)
	}
	if a.nick != "ggcode-bot" {
		t.Errorf("nick = %q, want %q", a.nick, "ggcode-bot")
	}
	if len(a.channels) != 2 {
		t.Fatalf("channels len = %d, want 2", len(a.channels))
	}
	if a.channels[0] != "#test" {
		t.Errorf("channels[0] = %q, want %q", a.channels[0], "#test")
	}
}

func TestNewIRCAdapter_Defaults(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra: map[string]interface{}{
			"nick": "ggcode-bot",
		},
	}
	a, err := newIRCAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.host != ircDefaultHost {
		t.Errorf("host = %q, want default %q", a.host, ircDefaultHost)
	}
	if a.port != ircDefaultPort {
		t.Errorf("port = %d, want default %d", a.port, ircDefaultPort)
	}
	if !a.useTLS {
		t.Error("TLS should be true by default")
	}
	if a.realName != "ggcode-bot" {
		t.Errorf("realName = %q, want %q", a.realName, "ggcode-bot")
	}
}

func TestNewIRCAdapter_TLSDisabled(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra: map[string]interface{}{
			"nick": "bot",
			"tls":  "false",
		},
	}
	a, err := newIRCAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.useTLS {
		t.Error("TLS should be false when explicitly disabled")
	}
}

func TestNewIRCAdapter_NickPassword(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra: map[string]interface{}{
			"nick":          "bot",
			"nick_password": "s3cret",
		},
	}
	a, err := newIRCAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.nickPass != "s3cret" {
		t.Errorf("nickPass = %q, want %q", a.nickPass, "s3cret")
	}
}

func TestNewIRCAdapter_Password(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra: map[string]interface{}{
			"nick":     "bot",
			"password": "server-password",
		},
	}
	a, err := newIRCAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.password != "server-password" {
		t.Errorf("password = %q, want %q", a.password, "server-password")
	}
}

func TestNewIRCAdapter_RealName(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra: map[string]interface{}{
			"nick":      "bot",
			"real_name": "GGCode IRC Bot",
		},
	}
	a, err := newIRCAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.realName != "GGCode IRC Bot" {
		t.Errorf("realName = %q, want %q", a.realName, "GGCode IRC Bot")
	}
}

func TestNewIRCAdapter_AdapterName(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "irc",
		Extra: map[string]interface{}{
			"nick": "bot",
		},
	}
	a, err := newIRCAdapter("my-irc", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "my-irc" {
		t.Errorf("Name() = %q, want %q", a.Name(), "my-irc")
	}
}

func TestParseIRCLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		prefix   string
		command  string
		trailing string
		params   int
	}{
		{
			name:     "PRIVMSG",
			line:     ":nick!user@host PRIVMSG #channel :hello world",
			prefix:   "nick!user@host",
			command:  "PRIVMSG",
			trailing: "hello world",
			params:   1,
		},
		{
			name:     "PING",
			line:     "PING :1234567890",
			command:  "PING",
			trailing: "1234567890",
			params:   0,
		},
		{
			name:     "RPL_WELCOME",
			line:     ":server 001 nick :Welcome to IRC",
			prefix:   "server",
			command:  "001",
			trailing: "Welcome to IRC",
			params:   1,
		},
		{
			name: "empty",
			line: "",
		},
		{
			name:    "PING no trailing",
			line:    "PING",
			command: "PING",
		},
		{
			name:     "NOTICE",
			line:     ":server NOTICE * :*** Looking up your hostname...",
			prefix:   "server",
			command:  "NOTICE",
			trailing: "*** Looking up your hostname...",
			params:   1,
		},
		{
			name:     "JOIN",
			line:     ":nick!user@host JOIN #channel",
			prefix:   "nick!user@host",
			command:  "JOIN",
			trailing: "",
			params:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := parseIRCLine(tt.line)
			if tt.line == "" {
				if msg != nil {
					t.Error("empty line should return nil")
				}
				return
			}
			if msg == nil {
				t.Fatalf("parseIRCLine(%q) returned nil", tt.line)
			}
			if msg.Prefix != tt.prefix {
				t.Errorf("prefix = %q, want %q", msg.Prefix, tt.prefix)
			}
			if msg.Command != tt.command {
				t.Errorf("command = %q, want %q", msg.Command, tt.command)
			}
			if msg.Trailing != tt.trailing {
				t.Errorf("trailing = %q, want %q", msg.Trailing, tt.trailing)
			}
			if len(msg.Params) != tt.params {
				t.Errorf("params len = %d, want %d (params=%v)", len(msg.Params), tt.params, msg.Params)
			}
		})
	}
}

func TestParseIRCPrefix(t *testing.T) {
	tests := []struct {
		prefix   string
		wantNick string
		wantUser string
		wantHost string
	}{
		{"alice!bob@example.com", "alice", "bob", "example.com"},
		{"irc.example.com", "irc.example.com", "", ""},
		{"nick!user", "nick", "user", ""},
	}
	for _, tt := range tests {
		nick, user, host := parseIRCPrefix(tt.prefix)
		if nick != tt.wantNick || user != tt.wantUser || host != tt.wantHost {
			t.Errorf("parseIRCPrefix(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.prefix, nick, user, host, tt.wantNick, tt.wantUser, tt.wantHost)
		}
	}
}

func TestSplitIRCMessage(t *testing.T) {
	// Short message
	chunks := splitIRCMessage("hello", 400)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("short message: %v", chunks)
	}

	// Long message without natural split point
	long := strings.Repeat("a", 800)
	chunks = splitIRCMessage(long, 400)
	if len(chunks) != 2 {
		t.Errorf("long no-break: expected 2 chunks, got %d", len(chunks))
	}
	rejoined := strings.Join(chunks, "")
	if rejoined != long {
		t.Error("rejoined != original")
	}

	// Long message with newline
	longNL := "line1\nline2\nline3\nline4\nline5"
	chunks = splitIRCMessage(longNL, 12)
	if len(chunks) < 2 {
		t.Errorf("long with newlines: expected 2+ chunks, got %d", len(chunks))
	}
	rejoined = strings.Join(chunks, "")
	if rejoined != longNL {
		t.Errorf("rejoined mismatch")
	}

	// Long message with spaces
	longSpace := strings.Repeat("word ", 200)
	chunks = splitIRCMessage(longSpace, 400)
	rejoined = strings.Join(chunks, "")
	if rejoined != longSpace {
		t.Errorf("space-split rejoined mismatch")
	}
}
