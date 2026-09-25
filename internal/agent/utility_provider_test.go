package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestUtilityProviderForWork pins the auxiliary-workload routing contract:
// without a routed utility provider, auxiliary LLM calls (autopilot
// strategist passes, reactive compaction summarization) run on the primary
// provider; installing one reroutes them; resetting to nil restores primary.
func TestUtilityProviderForWork(t *testing.T) {
	primary := &mockProvider{}
	a := NewAgent(primary, nil, "", 1)

	if got := a.utilityProviderForWork(); got != provider.Provider(primary) {
		t.Fatalf("without routing, utility work must use the primary provider, got %v", got)
	}
	if got := a.UtilityProvider(); got != nil {
		t.Fatalf("UtilityProvider() = %v, want nil before routing", got)
	}

	utility := &mockProvider{}
	a.SetUtilityProvider(utility)
	if got := a.UtilityProvider(); got != provider.Provider(utility) {
		t.Fatalf("UtilityProvider() = %v, want the routed provider", got)
	}
	if got := a.utilityProviderForWork(); got != provider.Provider(utility) {
		t.Fatalf("with routing, utility work must use the utility provider, got %v", got)
	}

	// Reset semantics: nil clears routing back to the primary provider.
	a.SetUtilityProvider(nil)
	if got := a.utilityProviderForWork(); got != provider.Provider(primary) {
		t.Fatalf("after reset, utility work must fall back to the primary provider, got %v", got)
	}
}
