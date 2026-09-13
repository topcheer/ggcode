package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #2244 probes.

// Case A: a multi-line job command (comment first line per repo
// convention) must survive extraction intact AND be classified read-only
// by its real command line.
func Test2244MultiLineJobCommandExtractedWhole(t *testing.T) {
	tc := provider.ToolCallDelta{Name: "wait_command", Arguments: json.RawMessage(`{"job_id":"cmd-7"}`)}
	// The tool layer indents continuation lines (#2244 layer 1).
	content := "Job ID: cmd-7\nCommand: # tail the failing test log\n    grep -rn FAIL ./logs\nStatus: done\nDuration: 2s\n"
	got := causalCmdForGate(tc, content)
	want := "# tail the failing test log\ngrep -rn FAIL ./logs"
	if got != want {
		t.Fatalf("#2244-A: extraction got %q, want %q", got, want)
	}
	if !looksLikeReadCommand(got) {
		t.Fatalf("#2244-A: comment-first multi-line read command not exempted")
	}
}

// Case A layer 2 directly: the direct channel (start_command args) carries
// the raw multi-line string; looksLikeReadCommand must skip comment lines.
func Test2244LooksLikeReadCommandSkipsComments(t *testing.T) {
	if !looksLikeReadCommand("# build then check\ncat report.txt") {
		t.Fatal("comment-first multi-line read command not classified")
	}
	if looksLikeReadCommand("# harmless comment\ngo test ./...") {
		t.Fatal("non-read multi-line command wrongly exempted")
	}
}

// Case B: the symmetric arm's ambiguity probe must key on the ERROR
// reference, not the edit name (which is never a key - the probe was
// effectively always-strong).
func Test2244SymmetricArmAmbiguityProbe(t *testing.T) {
	// Build a small detector instance through the exported entry used by
	// the callers; here we exercise suffixTier2 semantics via
	// attributeFailure on a nested-suffix pair where the error reference
	// is ambiguous (two distinct normalized paths share the basename).
	ca := newCausalAttributionState()
	ca.recordEdit("edit_file", "/w/pkg/api/types.go", 1)
	ca.recordEdit("edit_file", "/w/internal/api/types.go", 2)
	hint := ca.attributeFailure("compile: internal/api/types.go:12: undefined: X\n(warning: not unique)")
	// Ambiguous basename must NOT be reported with the authoritative
	// "error output references this file" phrasing on the innocent edit.
	if hint != "" && containsStr(hint, "/w/pkg/api/types.go") && containsStr(hint, "references this file") {
		t.Fatalf("#2244-B: authoritative phrasing on ambiguous suffix arm:\n%s", hint)
	}
}
