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
}

func TestParseIRCLine(t *testing.T) {
	tests := []struct {
		line     string
		prefix   string
		command  string
		trailing string
	}{
		{
			line:     ":nick!user@host PRIVMSG #channel :hello world",
			prefix:   "nick!user@host",
			command:  "PRIVMSG",
			trailing: "hello world",
		},
		{
			line:     "PING :1234567890",
			command:  "PING",
			trailing: "1234567890",
		},
		{
			line:     ":server 001 nick :Welcome",
			prefix:   "server",
			command:  "001",
			trailing: "Welcome",
		},
		{
			line: "",
		},
	}
	for _, tt := range tests {
		msg := parseIRCLine(tt.line)
		if tt.line == "" {
			if msg != nil {
				t.Errorf("empty line should return nil")
			}
			continue
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
	}
}

func TestParseIRCPrefix(t *testing.T) {
	nick, user, host := parseIRCPrefix("alice!bob@example.com")
	if nick != "alice" || user != "bob" || host != "example.com" {
		t.Errorf("parseIRCPrefix = %q %q %q, want alice bob example.com", nick, user, host)
	}
	nick, user, host = parseIRCPrefix("irc.example.com")
	if nick != "irc.example.com" || user != "" || host != "" {
		t.Errorf("parseIRCPrefix(server) = %q %q %q, want irc.example.com empty empty", nick, user, host)
	}
}

func TestSplitIRCMessage(t *testing.T) {
	short := "hello"
	chunks := splitIRCMessage(short, 400)
	if len(chunks) != 1 || chunks[0] != short {
		t.Errorf("short message: %v", chunks)
	}

	long := strings.Repeat("a", 800)
	chunks = splitIRCMessage(long, 400)
	if len(chunks) != 2 {
		t.Errorf("long message should split into 2, got %d", len(chunks))
	}
	rejoined := strings.Join(chunks, "")
	if rejoined != long {
		t.Error("rejoined != original")
	}
}
