package tool

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// #4553 companion tests: three independently confirmed silent-mismatch bugs.

// A: macro define args must be a JSON OBJECT - the old syntax-only probe
// accepted arrays/strings/numbers and exploded at run time instead.
func TestIssue4553A_MacroDefineRejectsNonObjectArgs(t *testing.T) {
	mt := &MacroTool{WorkingDir: t.TempDir(), Registry: NewRegistry()}
	out, err := mt.Execute(context.Background(), json.RawMessage(
		`{"action":"define","name":"bad","steps":[{"tool":"read_file","args":["/etc/hosts"]}]}`))
	if err == nil && !out.IsError {
		t.Fatalf("array args must be rejected at define time: %+v", out)
	}
	if !strings.Contains(out.Content, "args must be a JSON object") {
		t.Fatalf("unexpected rejection text: %q", out.Content)
	}
}

// B: kitty split size out of 1-99 must fail loudly, not silently split
// unresized. Validation fires before any kitty invocation.
func TestIssue4553B_KittySplitRejectsOutOfRangeSize(t *testing.T) {
	k := &KittyTool{}
	for _, size := range []int{100, 150, -5} {
		out := k.executeSplit(context.Background(), 1, "right", size, "", "")
		if !out.IsError {
			t.Fatalf("size=%d must be rejected, got: %+v", size, out)
		}
		if !strings.Contains(out.Content, "1-99") {
			t.Fatalf("size=%d rejection must state the valid range: %q", size, out.Content)
		}
	}
}

// C: "*" anywhere in the recipient list means broadcast-to-all. A mesh
// harness is out of scope for the minimal fix - source-pin the loop shape
// (the old single-element check silently dropped "*" from mixed lists).
func TestIssue4553C_WildcardAnywhereBroadcasts(t *testing.T) {
	raw, err := os.ReadFile("lanchat_tool.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	anchor := strings.Index(src, "doBroadcastAll(ctx, content, asAgent)")
	if anchor < 0 {
		t.Fatal("broadcast call not found - lanchat_tool.go drifted")
	}
	window := src[max(0, anchor-600):anchor]
	if !strings.Contains(window, "for _, id := range toNodeIDs") || !strings.Contains(window, `id == "*"`) {
		t.Fatal("wildcard broadcast must scan every recipient (mixed lists keep broadcast intent) (#4553-C)")
	}
}
