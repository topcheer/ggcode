package agent

import (
	"strings"
	"testing"
)

func TestDiversityState_Reset(t *testing.T) {
	d := newDiversityState()
	d.recordCall("edit_file")
	d.recordCall("edit_file")
	d.fired = true
	d.totalCalls = 5
	// Simulate a pending dominance flag (issue #312 sticky state).
	d.pending = true
	d.pendingCat = "edit"
	d.pendingCount = 10
	d.pendingTotal = 10

	d.reset()

	if d.fired != false {
		t.Error("fired should be false after reset")
	}
	if d.totalCalls != 0 {
		t.Error("totalCalls should be 0 after reset")
	}
	if len(d.window) != 0 {
		t.Error("window should be empty after reset")
	}
	if d.pending != false {
		t.Error("pending should be false after reset")
	}
	if d.pendingCat != "" || d.pendingCount != 0 || d.pendingTotal != 0 {
		t.Errorf("pending snapshot fields should be zeroed after reset, got cat=%q count=%d total=%d",
			d.pendingCat, d.pendingCount, d.pendingTotal)
	}
}

// TestDiversityState_PendingConsumeOnce (issue #312): a pending dominance set
// by recordCall must be consumed by exactly one check and not re-fire.
func TestDiversityState_PendingConsumeOnce(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 10; i++ {
		d.recordCall("edit_file")
	}
	if !d.pending {
		t.Fatal("expected pending flag set after 10 dominant edit calls")
	}
	// Flush the window with diverse calls; pending must survive.
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			d.recordCall("read_file")
		} else {
			d.recordCall("grep")
		}
	}

	g1 := d.check()
	if g1 == "" {
		t.Fatal("check should consume pending dominance and fire")
	}
	if !strings.Contains(g1, "edit") {
		t.Errorf("guidance should mention edit category: %s", g1)
	}
	if d.pending {
		t.Error("pending should be cleared after consumption")
	}
	// Second check must not re-fire (fired latch).
	if g2 := d.check(); g2 != "" {
		t.Errorf("second check should not re-fire, got: %s", g2)
	}
}

func TestDiversityState_NotEnoughCalls(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 5; i++ {
		d.recordCall("edit_file")
	}
	if g := d.check(); g != "" {
		t.Error("should not fire with < diversityMinCalls")
	}
}

func TestDiversityState_WindowNotFull(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 7; i++ {
		d.recordCall("edit_file")
	}
	// totalCalls=7 but window may be < 10
	if g := d.check(); g != "" {
		t.Error("should not fire when window not full")
	}
}

func TestDiversityState_EditDominance(t *testing.T) {
	d := newDiversityState()
	// 10 edits, 0 anything else
	for i := 0; i < 10; i++ {
		d.recordCall("edit_file")
	}
	g := d.check()
	if g == "" {
		t.Fatal("should fire with 10/10 edit calls")
	}
	if !strings.Contains(g, "edit") {
		t.Errorf("guidance should mention edit category: %s", g)
	}
	if !strings.Contains(g, "Tool Diversity Alert") {
		t.Error("should contain alert tag")
	}
}

func TestDiversityState_SearchDominance(t *testing.T) {
	d := newDiversityState()
	// 8 searches + 2 reads
	for i := 0; i < 8; i++ {
		d.recordCall("grep")
	}
	d.recordCall("read_file")
	d.recordCall("read_file")
	g := d.check()
	if g == "" {
		t.Fatal("should fire with 8/10 search calls")
	}
	if !strings.Contains(g, "search") {
		t.Errorf("guidance should mention search category: %s", g)
	}
}

func TestDiversityState_CommandDominance(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 7; i++ {
		d.recordCall("run_command")
	}
	d.recordCall("read_file")
	d.recordCall("read_file")
	d.recordCall("edit_file")
	g := d.check()
	if g == "" {
		t.Fatal("should fire with 7/10 command calls")
	}
	if !strings.Contains(g, "command") {
		t.Errorf("guidance should mention command category: %s", g)
	}
}

func TestDiversityState_ReadDominance(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 8; i++ {
		d.recordCall("read_file")
	}
	d.recordCall("grep")
	d.recordCall("grep")
	g := d.check()
	if g == "" {
		t.Fatal("should fire with 8/10 read calls")
	}
	if !strings.Contains(g, "read") {
		t.Errorf("guidance should mention read category: %s", g)
	}
}

func TestDiversityState_BalancedNoFire(t *testing.T) {
	d := newDiversityState()
	// Balanced mix, no dominance
	for i := 0; i < 3; i++ {
		d.recordCall("read_file")
	}
	for i := 0; i < 3; i++ {
		d.recordCall("edit_file")
	}
	for i := 0; i < 2; i++ {
		d.recordCall("run_command")
	}
	d.recordCall("grep")
	d.recordCall("grep")
	if g := d.check(); g != "" {
		t.Errorf("should NOT fire with balanced usage: %s", g)
	}
}

func TestDiversityState_FiresOncePerRun(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 10; i++ {
		d.recordCall("edit_file")
	}
	g1 := d.check()
	if g1 == "" {
		t.Fatal("first check should fire")
	}
	// Add more calls and check again
	for i := 0; i < 5; i++ {
		d.recordCall("edit_file")
	}
	g2 := d.check()
	if g2 != "" {
		t.Error("second check should NOT fire (already fired)")
	}
}

func TestDiversityState_SlidingWindow(t *testing.T) {
	d := newDiversityState()
	// Start with 10 edits (would trigger)
	for i := 0; i < 10; i++ {
		d.recordCall("edit_file")
	}
	// Now do 10 reads -- the window should slide past the edits
	for i := 0; i < 10; i++ {
		d.recordCall("read_file")
	}
	// Window is now all reads, totalCalls=20
	g := d.check()
	if g == "" {
		t.Fatal("should fire with read dominance in sliding window")
	}
	if !strings.Contains(g, "read") {
		t.Errorf("should mention read: %s", g)
	}
}

func TestDiversityState_MixedReadsBeforeEdit(t *testing.T) {
	d := newDiversityState()
	// 6 reads + 4 edits -- 60% read, below 70% threshold
	for i := 0; i < 6; i++ {
		d.recordCall("read_file")
	}
	for i := 0; i < 4; i++ {
		d.recordCall("edit_file")
	}
	if g := d.check(); g != "" {
		t.Errorf("60%% read should not fire (below 70%%): %s", g)
	}
}

func TestDiversityToolCategory(t *testing.T) {
	tests := []struct {
		tool string
		cat  string
	}{
		{"edit_file", "edit"},
		{"multi_edit_file", "edit"},
		{"write_file", "edit"},
		{"grep", "search"},
		{"search_files", "search"},
		{"code_search", "search"},
		{"read_file", "read"},
		{"multi_file_read", "read"},
		{"list_directory", "read"},
		{"run_command", "command"},
		{"start_command", "command"},
		{"review_changes", "verify"},
		{"code_health", "verify"},
		{"lsp_diagnostics", "verify"},
		{"unknown_tool", "other"},
	}

	for _, tt := range tests {
		got := diversityToolCategory(tt.tool)
		if got != tt.cat {
			t.Errorf("diversityToolCategory(%q) = %q, want %q", tt.tool, got, tt.cat)
		}
	}
}

func TestDiversityState_SevenOfTenEdits(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 7; i++ {
		d.recordCall("edit_file")
	}
	d.recordCall("read_file")
	d.recordCall("read_file")
	d.recordCall("grep")
	g := d.check()
	if g == "" {
		t.Fatal("7/10 edits (70%%) should fire")
	}
}

func TestDiversityState_SixOfTenEditsNoFire(t *testing.T) {
	d := newDiversityState()
	for i := 0; i < 6; i++ {
		d.recordCall("edit_file")
	}
	d.recordCall("read_file")
	d.recordCall("read_file")
	d.recordCall("grep")
	d.recordCall("grep")
	if g := d.check(); g != "" {
		t.Errorf("6/10 edits (60%%) should NOT fire: %s", g)
	}
}
