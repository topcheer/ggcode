package webui

// #2188 regression: sensitiveExtraKey was a local THREE-word list while
// the single source has five - nostr private_key and dingtalk app_key
// rendered in cleartext over GET /api/im (same asset and shape as
// #2167, surface moved to webui); and maskIMConfig only handled
// top-level strings, so nested stt.api_key maps came back whole.

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestMaskIMConfigNestedAndKeyFamily(t *testing.T) {
	in := config.IMConfig{Adapters: map[string]config.IMAdapterConfig{
		"nostr": {Extra: map[string]interface{}{
			"private_key": "nsec1secret",
			"display":     "my relay",
			"stt": map[string]interface{}{
				"api_key": "sk-stt",
				"model":   "whisper",
			},
		}},
	}}
	out := maskIMConfig(in)
	a := out.Adapters["nostr"]
	if s, _ := a.Extra["private_key"].(string); s != "__unchanged__" {
		t.Fatalf("nostr private_key must be masked, got %v", a.Extra["private_key"])
	}
	if a.Extra["display"] != "my relay" {
		t.Fatalf("benign top-level value must pass, got %v", a.Extra["display"])
	}
	stt, ok := a.Extra["stt"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested map must stay a map, got %T", a.Extra["stt"])
	}
	if stt["api_key"] != "__unchanged__" {
		t.Fatalf("nested stt.api_key must be masked, got %v", stt["api_key"])
	}
	if stt["model"] != "whisper" {
		t.Fatalf("nested benign value must pass, got %v", stt["model"])
	}

	// JSON-shape sanity FIRST: restore mutates in place (by design).
	blob, _ := json.Marshal(out)
	if zz2188contains(string(blob), "nsec1secret") || zz2188contains(string(blob), "sk-stt") {
		t.Fatalf("masked JSON still carries secrets: %s", blob)
	}

	// Round-trip: a full-replace PUT of the masked blob must restore the
	// real values (deep sentinel revert).
	prev := in
	restored := restoreMaskedIMCredentials(prev, out)
	ra := restored.Adapters["nostr"]
	if ra.Extra["private_key"] != "nsec1secret" {
		t.Fatalf("top-level sentinel must deep-restore, got %v", ra.Extra["private_key"])
	}
	rstt, ok := ra.Extra["stt"].(map[string]interface{})
	if !ok || rstt["api_key"] != "sk-stt" {
		t.Fatalf("nested sentinel must deep-restore, got %v", ra.Extra["stt"])
	}
	var _ = blob
}

func zz2188contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
