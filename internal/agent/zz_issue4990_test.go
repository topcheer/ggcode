package agent

import (
	"strings"
	"testing"
)

// #4990: the claim-verify banners must not fire on failure text that is
// merely QUOTED inside a successful tool result. Verdict-family patterns
// are anchored to the start of a line; real verdict lines sit at column 0
// while quoted text carries indentation or surrounding prose.

func TestIssue4990QuotedVerdictTextDoesNotFireBanner(t *testing.T) {
	c := newClaimVerifyState()
	quoted := `[watchdog] Pattern matching does substring matching against patterns
  (including ` + "`exit=1`" + `, ` + "`exit status 1`" + `, ` + "`--- fail:`" + `, ` + "`fail\\t`" + `).
	table entry: {"exit status 1", "Command exited...", true},
	reproduced: a go test run ok, but the output quoted "panic:" text.`
	if got := c.check("run_command", quoted, false, "gh issue view 4990"); got != "" {
		t.Fatalf("quoted failure text must not fire the banner, got: %s", got)
	}
}

func TestIssue4990RealVerdictLinesStillFire(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"exit status line", "ok   pkg\nexit status 1"},
		{"go test fail line", "--- FAIL: TestX (0.00s)"},
		{"go test fail tab summary", "FAIL\tgithub.com/topcheer/ggcode/internal/agent\t0.5s"},
		{"panic line", "panic: runtime error: index out of range"},
	}
	for _, tc := range cases {
		c := newClaimVerifyState()
		if got := c.check("run_command", tc.content, false, "go test ./..."); got == "" {
			t.Fatalf("%s: expected banner for real verdict line %q", tc.name, tc.content)
		}
	}
}

func TestIssue4990IsErrorResultsStillSkipped(t *testing.T) {
	c := newClaimVerifyState()
	if got := c.check("run_command", "--- FAIL: TestX", true, "go test ./..."); got != "" {
		t.Fatalf("isError results are already surfaced by the tool layer, got: %s", got)
	}
}

// #4990 mode 3: stale unresolved errors must age out instead of resurfacing
// in the silent-error nudge long after the run moved past them.
func TestIssue4990SilentErrorStaleEntriesAgeOut(t *testing.T) {
	s := newSilentErrorState()
	s.recordToolError("run_command", "go build ./pkg", "build error snippet", 1)

	// A much later error arrives: the iteration-1 entry is now stale and
	// must not be reported alongside it.
	s.recordToolError("run_command", "go test ./pkg", "test error snippet", 12)
	for i := 0; i < silentErrorThreshold; i++ {
		if msg := s.recordToolAction("run_command", "go vet ./other"); msg != "" {
			if strings.Contains(msg, "build error snippet") {
				t.Fatalf("stale iteration-1 error must not resurface: %s", msg)
			}
		}
	}
}

func TestIssue4990SilentErrorFreshEntriesStillReported(t *testing.T) {
	s := newSilentErrorState()
	s.recordToolError("run_command", "go test ./pkg", "test error snippet", 5)
	s.recordToolError("run_command", "go test ./pkg2", "test error snippet 2", 6)
	fired := false
	for i := 0; i < silentErrorThreshold+2; i++ {
		if msg := s.recordToolAction("run_command", "go vet ./other"); msg != "" {
			fired = true
			if !strings.Contains(msg, "unaddressed tool error") {
				t.Fatalf("unexpected guidance shape: %s", msg)
			}
		}
	}
	if !fired {
		t.Fatal("fresh errors must still trigger the silent-error guidance")
	}
}
