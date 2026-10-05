package tui

// #3389 probes: the render fetch limit must equal the navigation domain
// (knightPanelItemCount) - a hardcoded 20 in render against 50 in nav let
// the cursor walk onto unrendered rows. Seed 25 semantic-memory entries
// and require the rendered right column to carry the 25th (invisible under
// the old 20-cut), with the nav domain reporting 25.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/knight"
)

func TestIssue3389_RenderDomainMatchesNavDomain(t *testing.T) {
	root := t.TempDir()
	k := knight.New(config.KnightConfig{Enabled: true}, root, root, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(k.Stop)

	for i := 0; i < 25; i++ {
		if err := k.RecordSemanticMemory("probe-kind", fmt.Sprintf("entry-3389-%02d marker-unique-tail", i), nil, ""); err != nil {
			t.Fatalf("RecordSemanticMemory %d: %v", i, err)
		}
	}

	m := newTestModel()
	m.SetKnight(k)
	m.knightPanel = newKnightPanel()

	if got := m.knightPanelItemCount("memory"); got != 25 {
		t.Fatalf("nav domain = %d, want 25", got)
	}

	// The rendered column must show the 25th entry (index 24) - under the
	// pre-fix hardcoded limit of 20 it was fetched but never rendered.
	out := m.renderKnightMemory(80)
	if !strings.Contains(out, "entry-3389-24") {
		t.Fatalf("render must include the 25th entry when the nav domain does; tail of render:\n%s", tailN(out, 400))
	}
}

func tailN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
