package agent

import (
	"os"
	"strings"
	"testing"
)

// TestIssue4583_CommandChannelGate pins #4583: the working-tree invalidation
// gate must cover the bash/powershell command channel in addition to
// run_command/start_command, so `bash -c "git checkout ..."` cannot bypass
// the stale-read detector.
func TestIssue4583_CommandChannelGate(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	// Locate the gate (same window probe style as #3864's gate-wired test).
	idx := strings.Index(string(src), `runCommandMutatesTree(string(tc.Arguments)))) && !result.IsError`)
	if idx < 0 {
		t.Fatal("WT invalidation gate not found - condition shape changed")
	}
	start := idx - 400
	if start < 0 {
		start = 0
	}
	window := string(src[start:idx])
	for _, want := range []string{`tc.Name == "run_command"`, `tc.Name == "start_command"`, `tc.Name == "bash"`, `tc.Name == "powershell"`} {
		if !strings.Contains(window, want) {
			t.Fatalf("gate must enumerate command channel %s (#4583)", want)
		}
	}
	// The shared classifier must still flag a mutating git command.
	if !runCommandMutatesTree(`{"command":"git checkout other-branch"}`) {
		t.Fatal("runCommandMutatesTree must classify git checkout as tree-mutating")
	}
}
