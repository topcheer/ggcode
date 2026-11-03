package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3851_WriteInvalidatesSeenReadOnly(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	regAt := strings.Index(src, "seenReadOnly[dedupK] = len(toolResults) - 1")
	invAt := strings.Index(src, "seenReadOnly = make(map[dedupKey]int)")
	if regAt < 0 {
		t.Fatal("seenReadOnly registration site not found (dedup removed?)")
	}
	if invAt < 0 {
		t.Fatal("#3851: no write-invalidation of seenReadOnly found - a successful write leaves stale pre-write snapshots servable to later reads in the same batch")
	}
	if invAt < regAt {
		t.Fatal("invalidation must come after the registration site in the execution loop")
	}

	branch := src[regAt:invAt]
	if !strings.Contains(branch, "!result.IsError") {
		t.Fatal("invalidation branch must require a successful (non-error) result")
	}
	if !strings.Contains(branch, "speculativeSafeTools[tc.Name]") {
		t.Fatal("invalidation branch must exclude speculativeSafeTools (read-only) tools")
	}
}
