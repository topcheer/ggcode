package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func onboardTestConfig(t *testing.T, withKey bool) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEEPSEEK_API_KEY", "")

	path := filepath.Join(home, ".ggcode", "ggcode.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.FilePath = path
	vc := cfg.Vendors["deepseek"]
	if withKey {
		vc.APIKey = "sk-onboarded-key"
	}
	cfg.Vendors["deepseek"] = vc
	cfg.Vendor, cfg.Endpoint, cfg.Model = "deepseek", "api", "deepseek-chat"
	cfg.FirstRun = false
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return cfg
}

// The reported failure: the wizard finished, the config file was written, but
// no key reached disk - and the user only saw 401 "API 密钥无效或未配置" on the
// next start. The outcome must be reported and recorded, not swallowed.
func TestPersistOnboardOutcome_WarnsAndMarksWhenKeyMissing(t *testing.T) {
	cfg := onboardTestConfig(t, false)

	var buf bytes.Buffer
	if err := persistOnboardOutcome(cfg, &buf); err != nil {
		t.Fatalf("persistOnboardOutcome: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"without a usable API key", "deepseek/api", "ggcode onboard", "DEEPSEEK_API_KEY", "keys.env"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning must mention %q, got:\n%s", want, out)
		}
	}

	// The marker must survive the restart, or #1865 would keep onboarding from
	// ever running again.
	reloaded, err := config.Load(cfg.FilePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reloaded.OnboardIncomplete {
		t.Fatal("the incomplete marker must be persisted")
	}
	if !reloaded.NeedsOnboard() {
		t.Fatal("the next start must offer onboarding again")
	}
}

func TestPersistOnboardOutcome_SilentWhenKeyPersisted(t *testing.T) {
	cfg := onboardTestConfig(t, true)

	var buf bytes.Buffer
	if err := persistOnboardOutcome(cfg, &buf); err != nil {
		t.Fatalf("persistOnboardOutcome: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("no warning expected for a configured provider, got:\n%s", buf.String())
	}
	reloaded, err := config.Load(cfg.FilePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.OnboardIncomplete {
		t.Error("a successful onboarding must not leave the marker behind")
	}
	if reloaded.NeedsOnboard() {
		t.Error("a configured provider must not re-onboard")
	}
}
