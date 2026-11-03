package tool

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

type CascadeHintTracker struct {
	mu   sync.Mutex
	seen map[string]bool
}

func NewCascadeHintTracker() *CascadeHintTracker {
	return &CascadeHintTracker{seen: make(map[string]bool)}
}

func (t *CascadeHintTracker) Mark(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = make(map[string]bool)
	}
	if t.seen[id] {
		return false
	}
	t.seen[id] = true
	return true
}

func ParentModelFromProviderGetter(getter func() provider.Provider) func() string {
	return func() string {
		if getter == nil {
			return ""
		}
		if prov := getter(); prov != nil {
			if mp, ok := prov.(provider.ModelNameProvider); ok {
				return mp.ModelName()
			}
		}
		return ""
	}
}

func cascadeEscalationHint(snap subagent.Snapshot, parentModel string) string {
	if snap.Status != subagent.StatusFailed {
		return ""
	}
	runModel := strings.TrimSpace(snap.Model)
	parent := strings.TrimSpace(parentModel)
	if runModel == "" || parent == "" || strings.EqualFold(runModel, parent) {
		return ""
	}
	return fmt.Sprintf(
		"    [cascade escalation] This sub-agent ran on alternate model %q (your current model: %q). Its failure may reflect %q's capability limit rather than task infeasibility. Before concluding that, escalate once: re-run the task on your own model (spawn_agent without model=, or use_namedagent with model=%q).",
		runModel, parent, runModel, parent)
}

func maybeCascadeHint(tracker *CascadeHintTracker, snap subagent.Snapshot, parentModel string) string {
	hint := cascadeEscalationHint(snap, parentModel)
	if hint == "" {
		return ""
	}
	if tracker == nil || !tracker.Mark(snap.ID) {
		return ""
	}
	return hint
}

func appendCascadeHint(tracker *CascadeHintTracker, parentModel func() string, snap subagent.Snapshot) string {
	out := formatSubAgentSnapshot(snap)
	var parent string
	if parentModel != nil {
		parent = parentModel()
	}
	if hint := maybeCascadeHint(tracker, snap, parent); hint != "" {
		out += "\n" + hint
	}
	return out
}
