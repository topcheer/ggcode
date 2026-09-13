package main

// #2191 regression: maskedAdapterExtra only checked TOP-LEVEL keys, so
// the five adapters' nested stt overrides (apiKey/api_key) printed in
// cleartext on all three CLI read paths (show --json, show text via %v
// on the map, list --json).

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMaskedAdapterExtraRecursesNested(t *testing.T) {
	extra := map[string]interface{}{
		"private_key": "nsec1xyz",
		"display":     "ok",
		"stt": map[string]interface{}{
			"api_key": "sk-nested",
			"apiKey":  "sk-camel",
			"model":   "whisper",
		},
	}
	masked := maskedAdapterExtra(extra)

	if s, _ := masked["private_key"].(string); !strings.Contains(s, "*") || strings.Contains(s, "nsec1xyz") {
		t.Fatalf("top-level secret must mask: %v", masked["private_key"])
	}
	if masked["display"] != "ok" {
		t.Fatalf("benign top-level must pass: %v", masked["display"])
	}
	stt, ok := masked["stt"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested must stay a map, got %T", masked["stt"])
	}
	for _, c := range []struct{ k, full string }{{"api_key", "sk-nested"}, {"apiKey", "sk-camel"}} {
		if s, _ := stt[c.k].(string); s == c.full {
			t.Fatalf("nested %s must mask, got full plaintext %v", c.k, stt[c.k])
		}
	}
	if stt["model"] != "whisper" {
		t.Fatalf("nested benign must pass: %v", stt["model"])
	}
	// JSON path carries no secrets.
	blob, _ := json.Marshal(masked)
	if strings.Contains(string(blob), "nsec1xyz") || strings.Contains(string(blob), "sk-nested****FULL") || strings.Contains(string(blob), "sk-camel****FULL") {
		t.Fatalf("masked JSON still carries secrets: %s", blob)
	}
}
