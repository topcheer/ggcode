package tool

import "testing"

// TestRmLongFlagDispersed verifies #384: dispersed long-flag rm forms with
// the target path between the flags must hit block/ask, not fall through
// to Allow.
func TestRmLongFlagDispersed(t *testing.T) {
	g := NewCommandGate()
	r := g.Check("rm --force /etc --recursive")
	if r.Behavior != Block && r.Behavior != Ask {
		t.Fatalf("expected block/ask for dispersed long flags, got %v", r.Behavior)
	}
	r2 := g.Check("rm -fr /etc")
	if r2.Behavior != Block {
		t.Fatalf("short form expected block, got %v", r2.Behavior)
	}
}
