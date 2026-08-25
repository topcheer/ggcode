package webui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #1021: IM read routes must mask adapter credentials, and the full-replace
// PUT must restore sentinel-masked fields so masks never persist.

func TestMaskIMConfigMasksCredentials(t *testing.T) {
	cfg := config.IMConfig{Adapters: map[string]config.IMAdapterConfig{
		"slack": {
			Enabled: true,
			Env:     map[string]string{"TOKEN": "xoxb-secret"},
			Extra:   map[string]interface{}{"bot_token": "xoxb-123", "signing_secret": "shhh", "channel": "#dev"},
		},
	}}
	masked := maskIMConfig(cfg)
	a := masked.Adapters["slack"]
	if a.Extra["bot_token"] != "__unchanged__" || a.Extra["signing_secret"] != "__unchanged__" {
		t.Fatalf("credentials not masked: %v", a.Extra)
	}
	if a.Extra["channel"] != "#dev" {
		t.Fatalf("non-sensitive key wrongly masked: %v", a.Extra)
	}
	if _, maskedEnv := a.Env["__masked__"]; !maskedEnv {
		t.Fatalf("env not masked: %v", a.Env)
	}
	// original untouched
	if cfg.Adapters["slack"].Extra["bot_token"] != "xoxb-123" {
		t.Fatal("maskIMConfig must not mutate the source config")
	}
}

func TestSensitiveExtraKey(t *testing.T) {
	for _, k := range []string{"bot_token", "token", "Bot_Token", "signing_secret", "app_secret", "password"} {
		if !sensitiveExtraKey(k) {
			t.Errorf("%q should be sensitive", k)
		}
	}
	for _, k := range []string{"channel", "webhook_base", "username"} {
		if sensitiveExtraKey(k) {
			t.Errorf("%q should not be sensitive", k)
		}
	}
}

func TestRestoreMaskedIMCredentialsRoundTrip(t *testing.T) {
	prev := config.IMConfig{Adapters: map[string]config.IMAdapterConfig{
		"slack": {Env: map[string]string{"A": "1"}, Extra: map[string]interface{}{"bot_token": "xoxb-123"}},
	}}
	// Simulate the frontend round-trip: read (masked), edit unrelated field, PUT back.
	edited := maskIMConfig(prev)
	edited.Adapters["slack"].Extra["channel"] = "#ops"

	restored := restoreMaskedIMCredentials(prev, edited)
	a := restored.Adapters["slack"]
	if a.Extra["bot_token"] != "xoxb-123" {
		t.Fatalf("sentinel not restored: %v", a.Extra)
	}
	if a.Env["A"] != "1" {
		t.Fatalf("env marker not restored: %v", a.Env)
	}
	if a.Extra["channel"] != "#ops" {
		t.Fatalf("real edit lost: %v", a.Extra)
	}
}

// #1020: the /api/config sanitize path must also mask token/secret keys
// nested in adapter Extra maps.
func TestSanitizeMapMasksTokenSecret(t *testing.T) {
	m := map[string]interface{}{
		"im": map[string]interface{}{
			"adapters": map[string]interface{}{
				"slack": map[string]interface{}{
					"extra": map[string]interface{}{
						"bot_token": "xoxb-123",
						"other":     "visible",
					},
				},
			},
		},
	}
	sanitizeMap(m)
	extra := m["im"].(map[string]interface{})["adapters"].(map[string]interface{})["slack"].(map[string]interface{})["extra"].(map[string]interface{})
	if extra["bot_token"] != "***" {
		t.Fatalf("bot_token not masked: %v", extra)
	}
	if extra["other"] != "visible" {
		t.Fatalf("non-sensitive key masked: %v", extra)
	}
}
