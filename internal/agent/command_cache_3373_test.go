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

func TestCommandCacheHit_DenyPolicyAbandonsHit(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Allow}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "build ok"})

	a.policy = &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Deny}

	_, hit := a.checkCommandCache(tc3373.Name, tc3373.Arguments)
	if !hit {
		t.Fatal("cache entry should still hit - the guard, not invalidation, must reject it")
	}
	if a.speculativeHitAllowed(context.Background(), tc3373) {
		t.Fatal("Deny policy must abandon the commandCache hit (parity with #1496/#1831)")
	}
}

func TestCommandCacheHit_PlanModeLeaksNoShellOutput(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Allow}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "secret build output"})

	a.policy = &stubDecisionPolicy{mode: permission.PlanMode, decision: permission.Deny}

	if a.speculativeHitAllowed(context.Background(), tc3373) {
		t.Fatal("plan-mode policy must block the cached command replay")
	}
}

func TestCommandCacheHit_AskPolicyRePrompts(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Ask}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "build ok"})

	if a.speculativeHitAllowed(context.Background(), tc3373) {
		t.Fatal("Ask policy must abandon the hit - only a clean Allow may serve a replay")
	}
}

func TestCommandCacheHit_CancelledContextFailsClosed(t *testing.T) {
	a := &Agent{commandCache: newCommandCache(), policy: &stubDecisionPolicy{mode: permission.AutoMode, decision: permission.Allow}}
	a.storeCommandResult(tc3373.Name, tc3373.Arguments, tool.Result{Content: "build ok"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.speculativeHitAllowed(ctx, tc3373) {
		t.Fatal("cancelled context must fail closed")
	}
}
