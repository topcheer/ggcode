package tui

// #2158 regression: the Env loop in enterIMEditSelect stored secret env
// values as PLAINTEXT into fieldValues while the Extra loop five lines
// above masked them - opening the edit panel alone leaked every env
// secret (already ExpandEnv-resolved to real values at load time) to
// shoulder-surfing/screen-recording.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIMEditSelectMasksEnvSecrets(t *testing.T) {
	m := newTestModel()
	m.config = &config.Config{}
	m.config.IM.Adapters = map[string]config.IMAdapterConfig{}
	m.config.IM.Adapters["qq-test-2158"] = config.IMAdapterConfig{
		Env: map[string]string{
			"BOT_TOKEN": "secret-bot-token-2158",
			"APP_ID":    "123456",
		},
	}
	s := m.enterIMEditSelect("qq-test-2158")

	v, ok := s.fieldValues["env.BOT_TOKEN"]
	if !ok {
		t.Fatal("env field missing from edit state")
	}
	if strings.Contains(v, "secret-bot-token-2158") {
		t.Fatalf("env secret rendered in cleartext: %q", v)
	}
	if v == "" {
		t.Fatal("masked value must not be empty (edit affordance)")
	}
	// Benign env keys stay readable.
	if s.fieldValues["env.APP_ID"] != "123456" {
		t.Fatalf("benign env value must stay plaintext, got %q", s.fieldValues["env.APP_ID"])
	}
}
