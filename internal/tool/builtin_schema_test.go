package tool

import (
	"encoding/json"
	"testing"
)

// TestBuiltinToolSchemasAreValidJSON guards against invalid JSON in any
// builtin tool schema (#5396): a malformed Parameters() literal is embedded
// verbatim into provider requests - the OpenAI Responses endpoint fails to
// marshal EVERY request, while Anthropic/OpenAI-chat/Gemini silently degrade
// the tool to an empty schema. A one-line brace slip therefore bricks a whole
// session, so every builtin schema must round-trip as valid JSON.
func TestBuiltinToolSchemasAreValidJSON(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterBuiltinTools(registry, nil, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("RegisterBuiltinTools: %v", err)
	}
	tools := registry.List()
	if len(tools) < 20 {
		t.Fatalf("expected a meaningful builtin tool set, got %d - registration may have silently failed", len(tools))
	}
	for _, tl := range tools {
		params := tl.Parameters()
		if len(params) == 0 {
			continue // tool without a schema is legitimate
		}
		var v map[string]any
		if err := json.Unmarshal(params, &v); err != nil {
			t.Errorf("tool %q: Parameters() is not valid JSON: %v\nschema: %.200s", tl.Name(), err, string(params))
		}
	}
}
