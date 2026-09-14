package tui

// #2319: the IM /cost & /usage twins read m.session.TokenUsage without
// the sessionMutex the local /cost twin takes (#1366-C). Source pin:
// both Summary functions must Lock/Unlock around the read.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2319IMSummariesTakeSessionLock(t *testing.T) {
	b, err := os.ReadFile("remote_commands.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, fn := range []string{"SessionCostSummary", "SessionUsageSummary"} {
		start := strings.Index(src, "func (d tuiSlashDeps) "+fn)
		if start < 0 {
			t.Fatalf("%s not found", fn)
		}
		end := strings.Index(src[start+1:], "\nfunc ")
		body := src[start : start+1+end]
		if !strings.Contains(body, "sessionMutex()") && strings.Contains(body, "mu.Lock()") || !strings.Contains(body, "Unlock()") {
			t.Fatalf("%s must take the session lock around the TokenUsage read", fn)
		}
		if !strings.Contains(body, "#2319") {
			t.Fatalf("%s must carry the issue annotation", fn)
		}
	}
}
