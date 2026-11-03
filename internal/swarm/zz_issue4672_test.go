package swarm

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4672_AllLoopUpdatesCarryCASGuard(t *testing.T) {
	data, err := os.ReadFile("idle_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

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
