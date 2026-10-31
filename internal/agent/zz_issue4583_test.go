package agent

import (
	"os"
	"strings"
	"testing"
)

// TestIssue4583_BashPowershellReachWtGate (#4583): the working-tree
// invalidation command-channel fallback must enumerate bash and powershell
// alongside run_command/start_command - a `bash`-carried `git checkout`
// between two reads of the same file must invalidate stale reads exactly
// like run_command's. Source-pinned on the gate (same style as the #3864
// companion): the argument-shape question (do bash/powershell args carry
// `command`?) is answered by the repo's own peer surfaces parsing all
// three channel tools uniformly via args["command"] (sfCommandArg,
// strategy_fixation.go:81; agent.go:5568/5613; verification_debt.go:137).
func TestIssue4583_BashPowershellReachWtGate(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	anchor := strings.Index(src, "runCommandMutatesTree(string(tc.Arguments))")
	if anchor < 0 {
		t.Fatal("gate anchor not found")
	}
	window := src[anchor-500 : anchor+100]
	for _, name := range []string{`tc.Name == "bash"`, `tc.Name == "powershell"`, `tc.Name == "start_command"`} {
		if !strings.Contains(window, name) {
			t.Fatalf("gate window must enumerate %s for the command-channel fallback (#4583)", name)
		}
	}
}
