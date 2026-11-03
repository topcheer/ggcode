package tool

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4602_EXDEVSuccessDropsRestoredSourceKey(t *testing.T) {
	b, err := os.ReadFile("file_ops.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	removeAllAt := strings.Index(src, "if err := os.RemoveAll(source); err != nil {")
	if removeAllAt < 0 {
		t.Fatal("EXDEV RemoveAll branch not found - file_ops.go drifted")
	}

	carryAt := strings.Index(src, "defaultFileTracker.RecordRead(destination)")
	if carryAt < 0 {
		t.Fatal("carry-over to destination not found - file_ops.go drifted")
	}
	between := src[removeAllAt:carryAt]
	if !strings.Contains(between, "RemoveTracking(source)") {
		t.Fatal("EXDEV success path must RemoveTracking(source) to drop the restored stale key (#4602)")
	}
	if !strings.Contains(between, "if srcSeen {") {
		t.Fatal("stale-key cleanup must be gated on srcSeen (only the restored key is dropped, #4445 semantics preserved)")
	}
}
