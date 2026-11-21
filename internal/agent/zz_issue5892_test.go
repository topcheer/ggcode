package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

func systemTextOf(m provider.Message) string {
	var b strings.Builder
	for _, blk := range m.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// TestUpdateSystemPromptNoDirectHeadWrite: #5892 drift finding - the
// UpdateSystemPrompt path must NOT write the head directly; it only
// invalidates the injection cache so the next run's
// maybeInjectStableSystemHead reassembles through the single canonical
// path (with temporal anchor + headParts). Direct writes made the head
// oscillate between bare-base and assembled forms, invalidating the
// cached prefix twice per base change.
func TestUpdateSystemPromptNoDirectHeadWrite(t *testing.T) {
	cm := context.NewManager(128000)
	a := &Agent{
		contextManager:   cm,
		baseSystemPrompt: "base-v1",
	}

	// Prime the head via the canonical assembly path.
	a.maybeInjectStableSystemHead()
	msgs := cm.Messages()
	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatalf("expected a system head after assembly, got %d msgs", len(msgs))
	}
	before := systemTextOf(msgs[0])

	// UpdateSystemPrompt must not change the head in place.
	a.UpdateSystemPrompt("base-v2")
	msgs = cm.Messages()
	if len(msgs) == 0 {
		t.Fatal("head vanished")
	}
	after := systemTextOf(msgs[0])
	if after != before {
		t.Fatalf("UpdateSystemPrompt must not rewrite the head directly:\nbefore=%q\nafter=%q", before, after)
	}
	if strings.Contains(after, "base-v2") {
		t.Fatal("new base must not appear in the head before re-assembly")
	}

	// The cache invalidation must let the next assembly pick up base-v2.
	a.maybeInjectStableSystemHead()
	msgs = cm.Messages()
	final := systemTextOf(msgs[0])
	if !strings.Contains(final, "base-v2") {
		t.Fatal("next assembly must carry the updated base")
	}
	if !strings.Contains(final, "Current date/time") {
		t.Fatal("canonical assembly must keep the temporal anchor prefix")
	}
	if final == after {
		t.Fatal("assembly after invalidation should differ from stale head")
	}
}
