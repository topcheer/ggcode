//go:build goolm

package im

import (
	"os"
	"strings"
	"testing"
)

// #4529 pin: publishState runs on the RTM read-loop goroutine and must
// snapshot botUserID under a.mu (authTest writes it on the connect/reconnect
// goroutine); the auth-OK debug.Log must not re-read the field after Unlock.
func TestIssue4529_BotUserIDReadsSynchronized(t *testing.T) {
	b, err := os.ReadFile("slack_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	idx := strings.Index(s, "func (a *slackAdapter) publishState")
	if idx < 0 {
		t.Fatal("publishState not found")
	}
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		end = len(s) - idx - 1
	}
	body := s[idx : idx+1+end]
	if !strings.Contains(body, "a.mu.RLock()") {
		t.Fatal("#4529 regression: publishState reads botUserID without the adapter lock")
	}
	if strings.Contains(s, `debug.Log("slack", "adapter=%s auth OK botUserID=%s", a.name, a.botUserID)`) {
		t.Fatal("#4529 regression: auth-OK log re-reads botUserID after Unlock")
	}
}
