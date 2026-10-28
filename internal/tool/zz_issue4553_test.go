//go:build goolm

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newTestMacroTool returns a MacroTool whose store lives under a temp dir so
// define validation can be exercised without touching the real workspace.
func newTestMacroTool(t *testing.T) *MacroTool {
	t.Helper()
	return &MacroTool{WorkingDir: t.TempDir()}
}

// Issue #4553-A: macro define must reject non-object args (JSON array/string/
// number) at definition time instead of letting run blow up later.
func TestIssue4553MacroDefineRejectsNonObjectArgs(t *testing.T) {
	mt := newTestMacroTool(t)

	for name, rawArgs := range map[string]string{
		"array":  `[1,2]`,
		"string": `"foo"`,
		"number": `42`,
		"bool":   `true`,
	} {
		steps := []map[string]any{
			{"tool": "read_file", "args": json.RawMessage(rawArgs)},
		}
		body, _ := json.Marshal(map[string]any{
			"action": "define",
			"name":   "m_" + name,
			"steps":  steps,
		})
		res, err := mt.Execute(context.Background(), body)
		if err != nil {
			t.Fatalf("%s: unexpected err: %v", name, err)
		}
		if !res.IsError {
			t.Errorf("%s: expected IsError for non-object args %s, got success: %s", name, rawArgs, res.Content)
		}
		if !strings.Contains(res.Content, "must be a JSON object") {
			t.Errorf("%s: error should mention JSON object requirement, got: %s", name, res.Content)
		}
	}

	// A proper object must still pass validation (reaching store write).
	ok, _ := json.Marshal([]map[string]any{
		{"tool": "read_file", "args": json.RawMessage(`{"path":"/tmp/x"}`)},
	})
	body, _ := json.Marshal(map[string]any{"action": "define", "name": "m_ok", "steps": json.RawMessage(ok)})
	res, err := mt.Execute(context.Background(), body)
	if err != nil {
		t.Fatalf("object args: unexpected err: %v", err)
	}
	if res.IsError {
		t.Errorf("object args should be accepted, got: %s", res.Content)
	}
}

// Issue #4553-B: kitty split size>100 must fail fast instead of silently
// dropping --bias; size=100 is a valid kitty bias and must be passed through.
func TestIssue4553KittySplitSizeRange(t *testing.T) {
	k := NewKittyTool(t.TempDir())

	res := k.executeSplit(context.Background(), 0, "right", 150, "", "")
	if !res.IsError {
		t.Errorf("size=150 must be rejected, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "size must be 1-100") {
		t.Errorf("size=150 error should state valid range, got: %s", res.Content)
	}

	// size=100: bias must be passed through now (previously silently
	// dropped). Point kitten at a bogus socket so the launch fails fast
	// instead of opening a real kitty window; the failure must NOT be the
	// range rejection.
	t.Setenv("KITTY_LISTEN_ON", "unix:/nonexistent/kitty-test.sock")
	res = k.executeSplit(context.Background(), 0, "right", 100, "", "echo")
	if res.IsError && !strings.Contains(res.Content, "kitty split failed") {
		t.Errorf("size=100 should not be range-rejected, got: %s", res.Content)
	}
}
