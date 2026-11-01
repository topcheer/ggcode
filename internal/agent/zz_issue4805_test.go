package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func coArgs(t *testing.T, branch, path string, create bool) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"branch": branch, "path": path, "create": create})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// #4805 (S-38): checking the shared main tree out to a non-main branch must
// carry the fleet-baseline advisory at SWITCH time (the #4803 guard only
// fires later, on validation runs).
func TestIssue4805_CheckoutAdvisoryOnSharedTree(t *testing.T) {
	a := &Agent{workingDir: "/Volumes/new/ggai/ggcode"}

	adv := a.checkoutAdvisory("git_checkout", coArgs(t, "fix-1234", "", false))
	if adv == "" {
		t.Fatal("switch to a fix branch in the shared tree must advise")
	}
	if !strings.Contains(adv, "fix-1234") || !strings.Contains(adv, "#4805") {
		t.Fatalf("advisory must name branch and tag, got %q", adv)
	}
	if a.checkoutAdvisory("git_checkout", coArgs(t, "fix-1234", "", true)) == "" {
		t.Fatal("create-and-switch must advise identically")
	}
}

// Silent cases: returning to the trunk, worktree paths (explicit or via
// agent cwd), other tools, missing branch arg.
func TestIssue4805_CheckoutAdvisorySilentCases(t *testing.T) {
	a := &Agent{workingDir: "/Volumes/new/ggai/ggcode"}

	if got := a.checkoutAdvisory("git_checkout", coArgs(t, "main", "", false)); got != "" {
		t.Fatalf("returning to trunk must stay silent, got %q", got)
	}
	if got := a.checkoutAdvisory("git_checkout", coArgs(t, "fix-1", "/repo/.ggcode/worktrees/w1", false)); got != "" {
		t.Fatalf("worktree path must stay silent, got %q", got)
	}
	if got := a.checkoutAdvisory("git_status", []byte(`{}`)); got != "" {
		t.Fatal("other tools must stay silent")
	}
	wa := &Agent{workingDir: "/repo/.ggcode/worktrees/w2"}
	if got := wa.checkoutAdvisory("git_checkout", coArgs(t, "fix-1", "", false)); got != "" {
		t.Fatalf("agent cwd inside a worktree must stay silent, got %q", got)
	}
	if got := a.checkoutAdvisory("git_checkout", []byte(`{}`)); got != "" {
		t.Fatal("missing branch must stay silent")
	}
}
