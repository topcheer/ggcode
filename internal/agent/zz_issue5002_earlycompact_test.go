package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5002 gap 2: sub-agent sessions pace differently from interactive ones -
// token usage grows monotonically with no user-turn boundaries, so a
// proactive pre-compact at ~70% of the (metadata-derived) window keeps them
// off the reactive overflow path. Agent.SetEarlyCompactRatio is the
// forwarding hook the sub-agent harness calls right after construction.

func TestIssue5002SetEarlyCompactRatioForwardsToManager(t *testing.T) {
	a := NewAgent(&fakeModelProvider{name: "glm-4"}, tool.NewRegistry(), "sys", 3)
	base := a.ContextManager().AutoCompactThreshold()
	if base <= 0 {
		t.Fatalf("baseline threshold must be positive, got %d", base)
	}

	a.SetEarlyCompactRatio(0.70)
	early := a.ContextManager().AutoCompactThreshold()
	window := a.ContextManager().ContextWindow()
	want := int(float64(window) * 0.70)
	if early != want {
		t.Fatalf("early threshold: got %d want %d (0.70*%d)", early, want, window)
	}
	if early >= base {
		t.Fatalf("early threshold (%d) must be below baseline (%d)", early, base)
	}
}

// The run-loop gate itself (maybeAutoCompact's tokens < threshold check) now
// sees the lowered threshold, so a sub-agent shaped context crosses the
// trigger at ~70% instead of ~90%. Verified at the threshold level: fill the
// manager to just past 70% but below 90% and confirm the manager reports
// over-threshold - this is exactly the comparison maybeAutoCompact performs.
func TestIssue5002EarlyThresholdCrossesBeforeReactiveBand(t *testing.T) {
	a := NewAgent(&fakeModelProvider{name: "glm-4"}, tool.NewRegistry(), "sys", 3)
	cm := a.ContextManager()
	window := cm.ContextWindow()
	base := cm.AutoCompactThreshold() // ~window - reserve (~90%)
	a.SetEarlyCompactRatio(0.70)
	early := cm.AutoCompactThreshold()

	// Fill to 80% of the window: above the early trigger, below the reactive
	// band. This is the exact token level where pre-fix runs kept going and
	// eventually hit the raw overflow error. (~3.5 chars per token heuristic.)
	fill := strings.Repeat("x", int(float64(window)*0.80*3.5))
	cm.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: fill}},
	})
	tokens := cm.TokenCount()
	if tokens < early {
		t.Fatalf("at 80%% fill (%d tokens) the early trigger (%d) must already be crossed", tokens, early)
	}
	if tokens >= base {
		t.Fatalf("80%% fill (%d) must still be below the reactive threshold (%d) - the band gap 2 targets", tokens, base)
	}
}

// Manager without early ratio must keep the legacy threshold (interactive
// agents are unaffected).
func TestIssue5002EarlyRatioDoesNotAffectDefaultAgents(t *testing.T) {
	a := NewAgent(&bareProvider{}, tool.NewRegistry(), "sys", 3)
	cm, ok := a.ContextManager().(*ctxpkg.Manager)
	if !ok {
		t.Fatalf("expected *ctxpkg.Manager, got %T", a.ContextManager())
	}
	if r := cm.EarlyCompactRatio(); r != 0 {
		t.Fatalf("interactive agents must keep the early ratio disabled, got %v", r)
	}
	base := cm.AutoCompactThreshold()
	// Legacy behavior: threshold == window - effective reserve, identical to a
	// freshly constructed manager with the same window and no early ratio.
	want := ctxpkg.NewManager(cm.ContextWindow()).AutoCompactThreshold()
	if base != want {
		t.Fatalf("default threshold must stay window-effective-reserve: got %d want %d", base, want)
	}
}
