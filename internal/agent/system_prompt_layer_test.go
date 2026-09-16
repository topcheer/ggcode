package agent

import "testing"

func TestAddSystemPromptLayerRegisterAndReplace(t *testing.T) {
	a := &Agent{}
	if len(a.systemPromptLayers) != 0 {
		t.Fatalf("fresh agent should have no layers, got %d", len(a.systemPromptLayers))
	}

	calls := 0
	a.AddSystemPromptLayer("resume-reconciliation", func() string {
		calls++
		return "note-v1"
	})
	if len(a.systemPromptLayers) != 1 {
		t.Fatalf("want 1 layer, got %d", len(a.systemPromptLayers))
	}
	if got := a.systemPromptLayers[0].fn(); got != "note-v1" || calls != 1 {
		t.Fatalf("layer callback misbehaving: got %q, calls=%d", got, calls)
	}

	// Re-registering the same name must replace, not append.
	a.AddSystemPromptLayer("resume-reconciliation", func() string { return "note-v2" })
	if len(a.systemPromptLayers) != 1 {
		t.Fatalf("re-register should replace by name, got %d layers", len(a.systemPromptLayers))
	}
	if got := a.systemPromptLayers[0].fn(); got != "note-v2" {
		t.Errorf("replaced layer should return note-v2, got %q", got)
	}

	// Distinct names append; nil fns are skipped at injection time by the
	// caller, registration still records them.
	a.AddSystemPromptLayer("other-layer", nil)
	if len(a.systemPromptLayers) != 2 || a.systemPromptLayers[1].name != "other-layer" {
		t.Fatalf("distinct name should append: %+v", a.systemPromptLayers)
	}
}
