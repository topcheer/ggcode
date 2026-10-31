package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// MAST I1: the parent's established constraints must ride along into
// delegated tasks; idempotent; non-delegation tools untouched.
func TestInheritDelegationConstraints(t *testing.T) {
	a := &Agent{constraintAmnesia: newConstraintAmnesiaState()}
	a.constraintAmnesia.constraints = []constraintEntry{
		{excerpt: "do not touch internal/audit/"},
		{excerpt: "keep commits small"},
	}

	tc := a.inheritDelegationConstraints(provider.ToolCallDelta{
		Name:      "spawn_agent",
		Arguments: json.RawMessage(`{"task":"refactor the utils"}`),
	})
	var args map[string]string
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if !strings.Contains(args["task"], inheritedConstraintsMarker) {
		t.Fatalf("task must carry the marker block, got %q", args["task"])
	}
	if !strings.Contains(args["task"], "do not touch internal/audit/") {
		t.Fatalf("constraint text must be inherited, got %q", args["task"])
	}
	if !strings.HasPrefix(args["task"], "refactor the utils") {
		t.Fatal("original task text must be preserved first")
	}

	// Idempotent: a second pass (retry/replay) does not double-append.
	tc2 := a.inheritDelegationConstraints(tc)
	if string(tc2.Arguments) != string(tc.Arguments) {
		t.Fatal("second pass must be a no-op (idempotent)")
	}

	// delegate maps to the prompt field.
	tcD := a.inheritDelegationConstraints(provider.ToolCallDelta{
		Name:      "delegate",
		Arguments: json.RawMessage(`{"prompt":"review please"}`),
	})
	var dArgs map[string]string
	json.Unmarshal(tcD.Arguments, &dArgs)
	if !strings.Contains(dArgs["prompt"], inheritedConstraintsMarker) {
		t.Fatal("delegate prompt must inherit constraints")
	}

	// Non-delegation tool: untouched.
	plain := provider.ToolCallDelta{Name: "read_file", Arguments: json.RawMessage(`{"path":"x"}`)}
	if got := a.inheritDelegationConstraints(plain); string(got.Arguments) != string(plain.Arguments) || got.Name != plain.Name {
		t.Fatal("non-delegation tool calls must pass through unchanged")
	}

	// No recorded constraints: untouched.
	b := &Agent{constraintAmnesia: newConstraintAmnesiaState()}
	if got := b.inheritDelegationConstraints(provider.ToolCallDelta{
		Name: "spawn_agent", Arguments: json.RawMessage(`{"task":"x"}`),
	}); string(got.Arguments) != `{"task":"x"}` {
		t.Fatal("no constraints = no injection")
	}

	// Literal null arguments (#4693 family): unchanged, no panic.
	if got := a.inheritDelegationConstraints(provider.ToolCallDelta{
		Name: "spawn_agent", Arguments: json.RawMessage(`null`),
	}); string(got.Arguments) != `null` {
		t.Fatal("null arguments must pass through")
	}

	// Cap at 8 constraints.
	a.constraintAmnesia.constraints = nil
	for i := 0; i < 12; i++ {
		a.constraintAmnesia.constraints = append(a.constraintAmnesia.constraints, constraintEntry{excerpt: "c" + string(rune('a'+i))})
	}
	tcC := a.inheritDelegationConstraints(provider.ToolCallDelta{
		Name: "spawn_agent", Arguments: json.RawMessage(`{"task":"x"}`),
	})
	var cArgs map[string]string
	json.Unmarshal(tcC.Arguments, &cArgs)
	if got := strings.Count(cArgs["task"], "\nc"); got > 8 {
		t.Fatalf("at most 8 constraint lines, got %d", got)
	}
	if !strings.Contains(cArgs["task"], "\nch") {
		t.Fatal("8-entry cap must keep ca..ch")
	}
	if strings.Contains(cArgs["task"], "\nci") {
		t.Fatal("beyond-cap constraints must be dropped")
	}
}
