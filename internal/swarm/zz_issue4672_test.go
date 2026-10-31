package swarm

import (
	"os"
	"strings"
	"testing"
)

// #4672: every board Update in the idle-runner main loop must carry the
// ExpectedStatus=inProgress CAS guard - an external terminal write (team
// review approve, manual /task, RestoreJSON restore) during execution must
// never be stomped by the runner's own failure/success write-backs. The
// rollback paths already had it (#2579/#4541); these four were missing.
func TestIssue4672_AllLoopUpdatesCarryCASGuard(t *testing.T) {
	data, err := os.ReadFile("idle_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	// The main executeTask loop region: from the claim (ExpectedStatus
	// pending) to the success write-back.
	start := strings.Index(text, "ExpectedStatus: &pending,")
	end := strings.Index(text, "// #2579: guard both updates")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("idle-runner loop region not found")
	}
	region := text[start:end]
	total := strings.Count(region, "tmMgr.Update(claimed.ID")
	guarded := strings.Count(region, "ExpectedStatus: &inProgress")
	if total == 0 {
		t.Fatal("no loop updates found in region")
	}
	if guarded != total {
		t.Fatalf("every loop Update must carry ExpectedStatus=&inProgress: %d of %d guarded (#4672)", guarded, total)
	}
}
