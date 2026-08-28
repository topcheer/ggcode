package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// stubDenyPolicy denies everything and reports a fixed mode.
type stubDenyPolicy struct {
	mode permission.PermissionMode
}

func (p *stubDenyPolicy) Check(toolName string, input json.RawMessage) (permission.Decision, error) {
	return permission.Deny, nil
}
func (p *stubDenyPolicy) Mode() permission.PermissionMode { return p.mode }
func (p *stubDenyPolicy) IsDangerous(command string) bool { return false }
func (p *stubDenyPolicy) AllowedPath(path string) bool    { return true }
func (p *stubDenyPolicy) AllowedPathForTool(toolName, path string) bool {
	return true
}
func (p *stubDenyPolicy) AllowCommandPattern(pattern string)                        {}
func (p *stubDenyPolicy) SetOverride(toolName string, decision permission.Decision) {}

// TestIssue1209_DenyMessageIncludesModeAttribution verifies that the
// policy-deny result names the current permission mode and, in plan mode,
// points at the self-rescue channels (switch_mode / exit_plan_mode). Without
// the mode in the message, a model dropped into plan mode misattributes the
// denials to its own parameters and enters a degenerate retry loop.
func TestIssue1209_DenyMessageIncludesModeAttribution(t *testing.T) {
	a := &Agent{policy: &stubDenyPolicy{mode: permission.PlanMode}}
	tc := provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{}`)}

	res := a.executeToolWithPermission(context.Background(), tc)
	if !res.IsError {
		t.Fatal("expected deny result to be an error")
	}
	for _, want := range []string{
		"current mode: plan",
		"Plan mode is read-only",
		"switch_mode",
		"exit_plan_mode",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("plan-mode deny message missing %q:\n%s", want, res.Content)
		}
	}

	// Non-plan mode: mode still named, but no plan-specific self-rescue line.
	a2 := &Agent{policy: &stubDenyPolicy{mode: permission.AutoMode}}
	res2 := a2.executeToolWithPermission(context.Background(), tc)
	if !strings.Contains(res2.Content, "current mode: auto") {
		t.Errorf("auto-mode deny message missing mode attribution:\n%s", res2.Content)
	}
	if strings.Contains(res2.Content, "Plan mode is read-only") {
		t.Errorf("auto-mode deny message should not contain plan-mode guidance:\n%s", res2.Content)
	}
}
