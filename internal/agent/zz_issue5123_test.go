package agent

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Issue #5123: initialContextWindow consulted only the hand-maintained
// static known_models.go table, so every new model generation rotted the
// table (glm-5.x shipped with zero entries and all sub-agents fell back
// to 128k, #5119). This pin freezes the new lookup order: weekly
// models.dev sync table first (single source of truth), static table as
// cold-start fallback, 128k default last.
type stubProvider5123 struct{ model string }

func (s *stubProvider5123) Name() string { return "stub" }
func (s *stubProvider5123) Chat(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, nil
}
func (s *stubProvider5123) ChatStream(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, nil
}
func (s *stubProvider5123) CountTokens(_ context.Context, _ []provider.Message) (int, error) {
	return 0, nil
}
func (s *stubProvider5123) ModelName() string { return s.model }

func TestIssue5123SyncTableWiredBeforeStatic(t *testing.T) {
	cases := []struct {
		model string
		want  int
		note  string
	}{
		// Acceptance #1: value resolves from the sync table - the same
		// glm-5.3 entry could be deleted from the static table and this
		// would still hold, because the sync lookup runs first.
		{"glm-5.3", 1048576, "base row via synced table"},
		// Vendor-exact rows resolve from the sync table alone; the static
		// table cannot match vendor-scoped input at all (full-string
		// HasPrefix against a bare model pattern).
		{"@cf/zai-org/glm-5.3", 1048576, "vendor-exact row, static-blind input"},
		// Watchdog note 1: vendor deployments disagree with base rows
		// (z-ai/glm-5.3-flashx=1048576 vs base=1000000) - vendor-exact
		// must win, not the (too-small) base row.
		{"z-ai/glm-5.3-flashx", 1048576, "vendor-exact wins over base row"},
		// Vendor-scoped input with no exact row falls back to the base
		// name via last path segment - still a synced-table resolution.
		{"@cf/zai-org/glm-5.3-flashx", 1000000, "segment fallback, static cannot resolve"},
		// Acceptance #2: unknown to both tables -> 128k unchanged.
		{"totally-unknown-model-xyz", 128000, "unknown falls to default"},
		{"", 128000, "empty model name falls to default"},
	}
	for _, tc := range cases {
		if got := initialContextWindow(&stubProvider5123{model: tc.model}); got != tc.want {
			t.Errorf("%s: initialContextWindow(%q) = %d, want %d (%s)", tc.note, tc.model, got, tc.want, tc.note)
		}
	}
}
