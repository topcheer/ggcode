package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// TestAppendStableSystemSectionRidesAssembly: #5892 round-2 - stable
// content (project-memory hints) must ride the assembled head via
// AppendStableSystemSection, never land as a bare system-role message at
// the head position. Bare landing forced a full-rewrite on first assembly
// for every agent (main + sub-agents + teammates).
func TestAppendStableSystemSectionRidesAssembly(t *testing.T) {
	cm := context.NewManager(128000)
	a := &Agent{
		contextManager:       cm,
		baseSystemPrompt:     "base",
		stableSystemSections: []string{},
	}

	// Stable section before any assembly: no head written at all.
	a.AppendStableSystemSection("PROJECT MEMORY HINT")
	msgs := cm.Messages()
	if len(msgs) != 0 {
		t.Fatalf("stable section must not write the head directly, got %d msgs", len(msgs))
	}

	// First assembly carries base + temporal + stable section in ONE write.
	a.maybeInjectStableSystemHead()
	msgs = cm.Messages()
	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatalf("expected system head after assembly, got %d msgs", len(msgs))
	}
	head := systemTextOf(msgs[0])
	for _, want := range []string{"base", "Current date/time", "PROJECT MEMORY HINT"} {
		if !strings.Contains(head, want) {
			t.Fatalf("assembled head missing %q", want)
		}
	}
	_ = provider.Message{}
}
