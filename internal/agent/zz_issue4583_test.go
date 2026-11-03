package agent

import (
	"os"
	"strings"
	"testing"
)

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
