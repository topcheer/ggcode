package tui

import (
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"

	"github.com/topcheer/ggcode/internal/config"
)

// The wizard used to advance past the API-key step with an empty field, write
// the config without a key, and report success - the next start then failed 401.
func TestValidateAPIKey_RequiresKeyForKeyedPreset(t *testing.T) {
	m := onboardModel{
		apiKeyInput:    textinput.New(),
		selectedVendor: config.VendorPreset{ID: "deepseek", DisplayName: "DeepSeek", NeedsAPIKey: true, APIKeyEnvHint: "GGCODE_TEST_ONBOARD_UNSET_KEY"},
	}
	t.Setenv("GGCODE_TEST_ONBOARD_UNSET_KEY", "")
	os.Unsetenv("GGCODE_TEST_ONBOARD_UNSET_KEY")

	if msg := m.validateAPIKey(); msg == "" {
		t.Fatal("an empty key must block advancing for a preset that needs one")
	} else if want := "DeepSeek"; !strings.Contains(msg, want) {
		t.Errorf("error should name the provider (%q), got %q", want, msg)
	}

	m.apiKeyInput.SetValue("sk-typed-by-user")
	if msg := m.validateAPIKey(); msg != "" {
		t.Errorf("a typed key must pass, got %q", msg)
	}
}

// A key supplied through the vendor's environment variable satisfies the step:
// that is how headless / systemd / CI installs provide it.
func TestValidateAPIKey_AcceptsEnvProvidedKey(t *testing.T) {
	t.Setenv("GGCODE_TEST_ONBOARD_ENV_KEY", "sk-from-env")
	m := onboardModel{
		apiKeyInput:    textinput.New(),
		selectedVendor: config.VendorPreset{ID: "deepseek", DisplayName: "DeepSeek", NeedsAPIKey: true, APIKeyEnvHint: "GGCODE_TEST_ONBOARD_ENV_KEY"},
	}
	if msg := m.validateAPIKey(); msg != "" {
		t.Errorf("an env-provided key must pass, got %q", msg)
	}
}

// Presets that need no key (local servers) and the custom provider stay
// permissive.
func TestValidateAPIKey_PermissiveCases(t *testing.T) {
	m := onboardModel{apiKeyInput: textinput.New(), selectedVendor: config.VendorPreset{ID: "local", DisplayName: "Local", NeedsAPIKey: false}}
	if msg := m.validateAPIKey(); msg != "" {
		t.Errorf("a keyless preset must pass, got %q", msg)
	}
	m.selectedVendor = config.VendorPreset{ID: "", DisplayName: "Custom"}
	if msg := m.validateAPIKey(); msg != "" {
		t.Errorf("the custom provider keeps the key optional, got %q", msg)
	}
}
