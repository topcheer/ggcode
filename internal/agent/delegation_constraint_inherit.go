package agent

import (
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// MAST I1 (Information Asymmetry) defense: a parent agent's established
// user constraints (constraintAmnesiaState) used to stay parent-only - a
// delegated sub-agent never learned "do not touch X", so especially in
// isolated worktrees it could freely edit forbidden files. The Berkeley
// "Why Do Multi-Agent LLM Systems Fail?" taxonomy classifies this as the
// textbook inter-agent failure; the fix is deterministic inheritance at
// the delegation boundary (zero LLM cost).

// delegationTaskField maps each delegation tool to the argument that
// carries the task text.
var delegationTaskField = map[string]string{
	"spawn_agent":    "task",
	"use_namedagent": "task",
	"delegate":       "prompt",
}

const inheritedConstraintsMarker = "[inherited-constraints]"

// snapshotConstraints returns the recorded constraint excerpts (nil-safe).
func (s *constraintAmnesiaState) snapshotConstraints() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.constraints))
	for _, c := range s.constraints {
		if t := strings.TrimSpace(c.excerpt); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// inheritDelegationConstraints appends the parent's binding constraints to
// the delegation task argument. Idempotent (marker short-circuits); any
// parse failure returns tc unchanged - inheritance must never break a call.
func (a *Agent) inheritDelegationConstraints(tc provider.ToolCallDelta) provider.ToolCallDelta {
	field, ok := delegationTaskField[tc.Name]
	if !ok {
		return tc
	}
	cs := a.constraintAmnesia.snapshotConstraints()
	if len(cs) == 0 {
		return tc
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(tc.Arguments, &args); err != nil || args == nil {
		return tc // includes literal-null arguments (#4693 family): nil map
	}
	raw, ok := args[field]
	if !ok {
		return tc
	}
	var task string
	if err := json.Unmarshal(raw, &task); err != nil {
		return tc
	}
	if strings.Contains(task, inheritedConstraintsMarker) {
		return tc // already inherited (retry / replay)
	}
	if len(cs) > 8 {
		cs = cs[:8] // bounded; excerpts are already excerpt-limited
	}
	var b strings.Builder
	b.WriteString(task)
	b.WriteString("\n\n")
	b.WriteString(inheritedConstraintsMarker)
	b.WriteString(" (BINDING - established by the user in the parent session; they apply to you too):\n")
	for i, c := range cs {
		b.WriteString(strings.TrimSpace(c))
		if i < len(cs)-1 {
			b.WriteString("\n")
		}
	}
	args[field], _ = json.Marshal(b.String())
	out, err := json.Marshal(args)
	if err != nil {
		return tc
	}
	tc.Arguments = out
	return tc
}
