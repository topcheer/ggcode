package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #3373: the deterministic commandCache hit branch in the tool-execution
// chain must be guarded by speculativeHitAllowed like the memo (#1831) and
// speculator (#1496) replay branches - a replayed result must never leak
// past a policy that would no longer Allow the command. The branch
// condition under test is `checkCommandCache(...) hit &&
// speculativeHitAllowed(ctx, tc)`.

// stubDecisionPolicy returns a fixed decision from Check.
type stubDecisionPolicy struct {
	mode     permission.PermissionMode
	decision permission.Decision
}

func (p *stubDecisionPolicy) Check(toolName string, input json.RawMessage) (permission.Decision, error) {
	return p.decision, nil
}
func (p *stubDecisionPolicy) Mode() permission.PermissionMode                           { return p.mode }
func (p *stubDecisionPolicy) IsDangerous(command string) bool                           { return false }
func (p *stubDecisionPolicy) AllowedPath(path string) bool                              { return true }
func (p *stubDecisionPolicy) AllowedPathForTool(toolName, path string) bool             { return true }
func (p *stubDecisionPolicy) AllowCommandPattern(pattern string)                        {}
func (p *stubDecisionPolicy) SetOverride(toolName string, decision permission.Decision) {}
func (p *stubDecisionPolicy) BlocksAutoApprove(toolName string, input json.RawMessage) bool {
	return false
}

var tc3373 = provider.ToolCallDelta{
	Name:      "run_command",
	Arguments: json.RawMessage(`{"command":"make verify-ci","working_dir":"/tmp/repo"}`),
}

// Regression (issue checklist 3): policy still Allow - the cached result is
// served through the guard, annotated as a cache hit.
func TestCommandCacheHit_AllowPolicyStillServesCache(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Allow}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "build ok"})

	cached, hit := a.checkCommandCache(tc3373.Name, tc3373.Arguments)
	if !hit {
		t.Fatal("expected cache hit before policy assertions")
	}
	if !a.speculativeHitAllowed(context.Background(), tc3373) {
		t.Fatal("Allow policy must let the hit through (regression: normal cache path broken)")
	}
	if !strings.Contains(cached.Content, "build ok") {
		t.Errorf("cached content drifted: %q", cached.Content)
	}
}

// Issue checklist 1: policy tightened to Deny mid-session (e.g. plan mode
// switch) - the hit must be abandoned so the chain falls back to gated
// execution instead of replaying shell output without a decision.
func TestCommandCacheHit_DenyPolicyAbandonsHit(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Allow}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "build ok"})

	// Mid-session tightening: same command now Denies.
	a.policy = &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Deny}

	_, hit := a.checkCommandCache(tc3373.Name, tc3373.Arguments)
	if !hit {
		t.Fatal("cache entry should still hit - the guard, not invalidation, must reject it")
	}
	if a.speculativeHitAllowed(context.Background(), tc3373) {
		t.Fatal("Deny policy must abandon the commandCache hit (parity with #1496/#1831)")
	}
}

// Issue checklist 2: plan-mode session - cached shell output must not be
// served to the model.
func TestCommandCacheHit_PlanModeLeaksNoShellOutput(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Allow}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "secret build output"})

	// User switches to plan mode: policy now denies shell execution.
	a.policy = &stubDecisionPolicy{mode: permission.PlanMode, decision: permission.Deny}

	if a.speculativeHitAllowed(context.Background(), tc3373) {
		t.Fatal("plan-mode policy must block the cached command replay")
	}
}

// The Ask decision is not Allow either: a tightening that turns the command
// into an Ask must re-prompt rather than silently replay.
func TestCommandCacheHit_AskPolicyRePrompts(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Ask}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "build ok"})

	if a.speculativeHitAllowed(context.Background(), tc3373) {
		t.Fatal("Ask policy must abandon the hit - only a clean Allow may serve a replay")
	}
}

// A cancelled context makes the guard fail closed, matching the memo and
// speculator branches.
func TestCommandCacheHit_CancelledContextFailsClosed(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Allow}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "build ok"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.speculativeHitAllowed(ctx, tc3373) {
		t.Fatal("cancelled context must fail closed")
	}
}
