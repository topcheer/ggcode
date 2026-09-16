package tool

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

// Cascade escalation hints: close the model-tier cascade loop for sub-agents.
//
// Research basis:
//   - FrugalGPT (Chen et al., Stanford, 2023): LLM cascades only pay off
//     when a cheap first stage is paired with an explicit escalation path
//     to a stronger model; without escalation the "cheap try" is wasted.
//   - arXiv:2410.10347 "A Unified Approach to Routing and Cascading for
//     LLMs" (2024): routing and cascading are complementary - cascades
//     need a well-defined escalation rule evaluated at quality boundaries.
//   - CascadeDebate (ACL 2026 industry track): engage escalation only at
//     boundaries, not proactively on every failure mode.
//   - HarnessDev (2026): harness/agent quality is executor-specific; a
//     sub-agent failing on a smaller model is frequently a capability
//     boundary of that executor rather than task infeasibility.
//
// Problem: ggcode already implements the "cheap first" half of a cascade -
// spawn_agent accepts a per-invocation model= parameter and named agent
// templates support a Model override. But the escalation half is missing:
// when a sub-agent fails on an alternate (typically cheaper) model, the
// parent sees a bare "failed" status with no signal that the failure may be
// model-capability-bound. In practice the parent either retries the same
// cheap tier (wasted cost), silently redoes the work itself at full price,
// or declares the task infeasible without ever trying the obvious
// escalation.
//
// Fix: when the parent retrieves a FAILED sub-agent that ran on a model
// different from the parent's current model, annotate the snapshot with a
// one-shot cascade escalation hint. Fired at most once per agent run (the
// parent re-polls terminal runs via wait_agent, and an unbounded hint would
// re-appear on every poll), and only at the failure boundary - never on
// running/completed/cancelled runs.

// CascadeHintTracker deduplicates cascade escalation hints per agent run.
// It is shared by the wait_agent and list_agents tools so a hint fires at
// most once regardless of which tool surfaces the failure first. Safe for
// concurrent use; Clone()d tool copies share the same tracker instance.
type CascadeHintTracker struct {
	mu   sync.Mutex
	seen map[string]bool
}

// NewCascadeHintTracker returns an empty tracker.
func NewCascadeHintTracker() *CascadeHintTracker {
	return &CascadeHintTracker{seen: make(map[string]bool)}
}

// Mark records id and reports whether this is the first time it was seen
// (i.e. whether a hint for this run should still be emitted).
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

// ParentModelFromProviderGetter adapts a provider getter into a parent
// model-name resolver, mirroring the SpawnAgentTool displayModel fallback:
// the live provider's ModelName when it implements ModelNameProvider, else
// empty. Never returns a non-nil function.
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

// cascadeEscalationHint returns the escalation annotation for a failed
// alternate-model sub-agent run, or "" when no cascade semantics apply.
// Applies only at the failure boundary (CascadeDebate: escalate at
// boundaries): terminal success and cancellations carry no hint, and runs
// on the parent's own model (including inherit, where snap.Model is empty)
// have no alternate executor to escalate away from.
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

// maybeCascadeHint computes the hint and consumes the one-shot budget for
// this agent run. Returns "" when no hint applies or it was already emitted.
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

// appendCascadeHint formats a snapshot, then appends the one-shot escalation
// hint when applicable. Shared by wait_agent and list_agents so both
// retrieval paths surface the hint consistently.
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
