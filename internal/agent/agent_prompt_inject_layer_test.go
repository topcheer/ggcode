package agent

import (
	"strings"
	"testing"
)

func TestCollectDynamicPromptLayersOrder(t *testing.T) {
	a := NewAgent(nil, nil, "base", 1)
	a.SetSystemPromptInjector(func() string { return "  injector-out  " })
	a.AddSystemPromptLayer("l1", func() string { return "layer-one" })
	a.AddSystemPromptLayer("l2", func() string { return "   " }) // blank layers are dropped
	a.AddSystemPromptLayer("l3", func() string { return "layer-three" })

	parts := a.collectDynamicPromptLayers(func() string { return "  injector-out  " })
	if len(parts) != 3 {
		t.Fatalf("want 3 non-empty layers, got %d: %q", len(parts), parts)
	}
	if parts[0] != "injector-out" {
		t.Fatalf("layer 2 (injector) should come first after goal, got %q", parts[0])
	}
	if parts[1] != "layer-one" || parts[2] != "layer-three" {
		t.Fatalf("named layers must preserve registration order, got %q", parts[1:])
	}
}

func TestCollectDynamicPromptLayersEmpty(t *testing.T) {
	a := NewAgent(nil, nil, "base", 1)
	if parts := a.collectDynamicPromptLayers(nil); len(parts) != 0 {
		t.Fatalf("no injector and no layers should yield no parts, got %q", parts)
	}
	// Whitespace-only injector output must not produce a layer.
	if parts := a.collectDynamicPromptLayers(func() string { return " \n\t " }); len(parts) != 0 {
		t.Fatalf("blank injector output should be dropped, got %q", parts)
	}
}

// TestMaybeInjectDynamicSystemPromptNoOpStable pins the change-detection
// contract of the Grounding stage: an unchanged prompt must not rebuild the
// lastInjectedSystemPrompt value (byte-identical across calls).
func TestMaybeInjectDynamicSystemPromptNoOpStable(t *testing.T) {
	a := NewAgent(nil, nil, "You are a coding assistant.", 1)
	a.maybeInjectDynamicSystemPrompt()
	first := a.lastInjectedSystemPrompt
	a.maybeInjectDynamicSystemPrompt()
	if a.lastInjectedSystemPrompt != first {
		t.Fatalf("second run must be a no-op for identical state:\nfirst:  %q\nsecond: %q", first, a.lastInjectedSystemPrompt)
	}
	if !strings.Contains(first, "You are a coding assistant.") {
		t.Fatalf("base prompt missing: %q", first)
	}
}
