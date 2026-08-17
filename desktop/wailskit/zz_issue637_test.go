//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func setupIssue637Home(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)
}

// saveIssue637Adapters writes adapter configs straight into ggcode.yaml the
// way a hand-edited file or prior UI save would leave them.
func saveIssue637Adapters(t *testing.T, adapters map[string]config.IMAdapterConfig) {
	t.Helper()
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.IM.Adapters = adapters
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// Defect 1a: the old `if field.Label == ""` skip was dead (every registry
// field has a Label), so optionals were hard-required. signal's base_url has
// a runtime default and irc's channels are optional — both must pass without
// those keys, while genuinely required keys must still be enforced.
func TestIssue637_OptionalFieldsNotRequired(t *testing.T) {
	setupIssue637Home(t)
	saveIssue637Adapters(t, map[string]config.IMAdapterConfig{
		"sig": {Enabled: true, Platform: "signal", Extra: map[string]interface{}{"account": "+15550001111"}},
		"irc": {Enabled: true, Platform: "irc", Extra: map[string]interface{}{"host": "irc.libera.chat:6697", "nick": "bot"}},
		// Control: a missing REQUIRED field must still fail.
		"qq-missing": {Enabled: true, Platform: "qq", Extra: map[string]interface{}{"appid": "123"}},
	})

	if err := TestIMConnection("sig"); err != nil {
		t.Fatalf("signal without optional base_url must pass: %v", err)
	}
	if err := TestIMConnection("irc"); err != nil {
		t.Fatalf("irc without optional channels must pass: %v", err)
	}
	err := TestIMConnection("qq-missing")
	if err == nil || !strings.Contains(err.Error(), "appsecret") {
		t.Fatalf("missing required qq appsecret must be reported, got: %v", err)
	}
}

// Defect 1b: stdio's Command lives on the adapter struct, not Extra — the
// Extra-only loop never checked it.
func TestIssue637_StdioCommandChecked(t *testing.T) {
	setupIssue637Home(t)
	saveIssue637Adapters(t, map[string]config.IMAdapterConfig{
		"no-cmd":   {Enabled: true, Platform: "privateclaw", Transport: "stdio"},
		"with-cmd": {Enabled: true, Platform: "privateclaw", Transport: "stdio", Command: "/usr/local/bin/privateclaw"},
	})

	err := TestIMConnection("no-cmd")
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("stdio adapter without Command must fail on command, got: %v", err)
	}
	if err := TestIMConnection("with-cmd"); err != nil {
		t.Fatalf("stdio adapter with Command must pass: %v", err)
	}
}

// Defect 2: SaveIMAdapter only checked non-empty platform; typos and
// whitespace platforms persisted fine and only blew up at Test time.
func TestIssue637_SaveValidatesPlatform(t *testing.T) {
	setupIssue637Home(t)

	err := SaveIMAdapter("typo", map[string]string{"platform": "telegarm"})
	if err == nil || !strings.Contains(err.Error(), "unknown platform") {
		t.Fatalf("typo platform must be rejected at save time, got: %v", err)
	}
	err = SaveIMAdapter("spaced", map[string]string{"platform": " telegram "})
	if err == nil || !strings.Contains(err.Error(), "unknown platform") {
		t.Fatalf("whitespace platform must be rejected at save time, got: %v", err)
	}

	// Valid and case-insensitive platforms still save (#591 compat).
	if err := SaveIMAdapter("ok", map[string]string{"platform": "telegram"}); err != nil {
		t.Fatalf("valid platform must save: %v", err)
	}
	if err := SaveIMAdapter("ok-ci", map[string]string{"platform": "Telegram"}); err != nil {
		t.Fatalf("case-insensitive platform must save: %v", err)
	}

	// Nothing was persisted by the rejected saves.
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.IM.Adapters["typo"]; exists {
		t.Fatal("rejected platform must not be persisted")
	}
	if _, exists := cfg.IM.Adapters["spaced"]; exists {
		t.Fatal("whitespace platform must not be persisted")
	}
}

// Registry sanity: the Required flags themselves.
func TestIssue637_RequiredFlags(t *testing.T) {
	sig := imPlatformByID("signal")
	if sig == nil {
		t.Fatal("signal missing from registry")
	}
	for _, f := range sig.Fields {
		if f.Key == "base_url" && f.Required {
			t.Fatal("signal base_url must not be required (runtime default)")
		}
		if f.Key == "account" && !f.Required {
			t.Fatal("signal account must be required")
		}
	}
	irc := imPlatformByID("irc")
	if irc == nil {
		t.Fatal("irc missing from registry")
	}
	for _, f := range irc.Fields {
		if f.Key == "channels" && f.Required {
			t.Fatal("irc channels must not be required")
		}
	}
}
