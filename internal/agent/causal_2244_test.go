package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func Test2244MultiLineJobCommandExtractedWhole(t *testing.T) {
	tc := provider.ToolCallDelta{Name: "wait_command", Arguments: json.RawMessage(`{"job_id":"cmd-7"}`)}

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

func Test2244LooksLikeReadCommandSkipsComments(t *testing.T) {
	if !looksLikeReadCommand("# build then check\ncat report.txt") {
		t.Fatal("comment-first multi-line read command not classified")
	}
	if looksLikeReadCommand("# harmless comment\ngo test ./...") {
		t.Fatal("non-read multi-line command wrongly exempted")
	}
}

func Test2244SymmetricArmAmbiguityProbe(t *testing.T) {

	ca := newCausalAttributionState()
	ca.recordEdit("edit_file", "/w/pkg/api/types.go", 1)
	ca.recordEdit("edit_file", "/w/internal/api/types.go", 2)
	hint := ca.attributeFailure("compile: internal/api/types.go:12: undefined: X\n(warning: not unique)")

	if hint != "" && containsStr(hint, "/w/pkg/api/types.go") && containsStr(hint, "references this file") {
		t.Fatalf("#2244-B: authoritative phrasing on ambiguous suffix arm:\n%s", hint)
	}
}
