package harness

import "testing"

func TestEnsureOperationalContextsAddsCrossCuttingWhenSignalsSuggestIt(t *testing.T) {
	contexts := []ContextConfig{
		{Name: "inventory", Path: "services/inventory", RequireAgent: true},
		{Name: "sales", Path: "services/sales", RequireAgent: true},
		{Name: "members", Path: "services/members", RequireAgent: true},
	}
	got := EnsureOperationalContexts(contexts, "Run full e2e and release validation", nil, nil)
	if len(got) != 4 {
		t.Fatalf("expected cross-cutting context to be added, got %#v", got)
	}
	if got[0].Name != crossCuttingContextName {
		t.Fatalf("expected normalized contexts to include %q, got %#v", crossCuttingContextName, got)
	}
}

func TestAugmentRunContextsAddsCrossCuttingForExistingBusinessContexts(t *testing.T) {
	contexts := []ContextConfig{
		{Name: "inventory", Path: "services/inventory", RequireAgent: true},
		{Name: "sales", Path: "services/sales", RequireAgent: true},
		{Name: "members", Path: "services/members", RequireAgent: true},
	}
	got := AugmentRunContexts(contexts, "deploy and run end-to-end verification")
	found := false
	for _, item := range got {
		if item.Name == crossCuttingContextName {
			found = true
			if item.Path != "" {
				t.Fatalf("expected cross-cutting context to stay pathless, got %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("expected augmented run contexts to include %q, got %#v", crossCuttingContextName, got)
	}
}
