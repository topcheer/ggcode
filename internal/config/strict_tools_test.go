package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// sa-60: strict_tools is an opt-in per-endpoint knob; YAML shape is pinned
// here so config documentation and resolution logic cannot silently drift.
func TestStrictToolsYAMLParse(t *testing.T) {
	var ep EndpointConfig
	in := []byte(`
strict_tools: true
strict_tools_allow:
  - grep
  - read_file
`)
	if err := yaml.Unmarshal(in, &ep); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if ep.StrictTools == nil || !*ep.StrictTools {
		t.Fatalf("strict_tools = %v, want true", ep.StrictTools)
	}
	if len(ep.StrictToolsAllow) != 2 || ep.StrictToolsAllow[0] != "grep" {
		t.Fatalf("strict_tools_allow = %v", ep.StrictToolsAllow)
	}

	// Absent knob must stay nil (disabled), never default-on.
	var off EndpointConfig
	if err := yaml.Unmarshal([]byte("max_tokens: 1024\n"), &off); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if off.StrictTools != nil {
		t.Fatalf("strict_tools should be nil when absent, got %v", *off.StrictTools)
	}
}
