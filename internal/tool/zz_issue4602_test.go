package tool

import (
	"os"
	"strings"
	"testing"
)

// TestIssue4602_EXDEVSuccessDropsRestoredSourceKey (#4602): after the EXDEV
// fallback's copy+RemoveAll succeed, the source key that the failure-entry
// restore re-created must be dropped - the tracker must not keep a read
// record for a path that no longer exists (recreating the file at the source
// path would then skip the #4448 blind-write warning). Source-pinned: a real
// EXDEV needs two filesystems; the guard's presence in the success branch is
// the contract.
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
	// The cleanup guard must sit after the RemoveAll error return, before the
	// read-state carry-over comment block.
	carryAt := strings.Index(src, "// Carry the read-state over to the new path")
	if carryAt < 0 {
		t.Fatal("carry-over comment not found - file_ops.go drifted")
	}
	between := src[removeAllAt:carryAt]
	if !strings.Contains(between, "RemoveTracking(source)") {
		t.Fatal("EXDEV success path must RemoveTracking(source) to drop the restored stale key (#4602)")
	}
	if !strings.Contains(between, "if srcSeen {") {
		t.Fatal("stale-key cleanup must be gated on srcSeen (only the restored key is dropped, #4445 semantics preserved)")
	}
}
