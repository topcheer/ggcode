package agent

import (
	"strconv"
	"strings"
	"testing"
)

func TestPatchExhaust_GiveUpThreshold(t *testing.T) {
	s := newPatchExhaustState()
	dir := "internal/agent"

	// Reads 1-3 should not trigger (below threshold of 4).
	for i := 1; i <= 3; i++ {
		hint := s.recordRead(dir + "/file" + strconv.Itoa(i) + ".go")
		if hint != "" {
			t.Fatalf("read %d should not trigger hint, got: %s", i, hint)
		}
	}

	// Read 4 hits the threshold.
	hint := s.recordRead(dir + "/file4.go")
	if hint == "" {
		t.Fatal("read 4 should trigger patch exhaustion hint")
	}
	if !strings.Contains(hint, "exhausted patch") {
		t.Errorf("hint should mention exhausted patch, got: %s", hint)
	}
}

func TestPatchExhaust_EditResets(t *testing.T) {
	s := newPatchExhaustState()
	dir := "pkg/foo"

	// 3 reads into same patch.
	for i := 1; i <= 3; i++ {
		s.recordRead(dir + "/f" + strconv.Itoa(i) + ".go")
	}
	// Edit resets the consecutive counter.
	s.recordEdit(dir + "/f1.go")

	// Now 3 more reads should not trigger (counter was reset).
	for i := 4; i <= 6; i++ {
		hint := s.recordRead(dir + "/f" + strconv.Itoa(i) + ".go")
		if hint != "" {
			t.Fatalf("read %d after edit should not trigger, got: %s", i, hint)
		}
	}
}

func TestPatchExhaust_DifferentPatchResets(t *testing.T) {
	s := newPatchExhaustState()

	// 3 reads into dir A.
	for i := 1; i <= 3; i++ {
		s.recordRead("dirA/f" + strconv.Itoa(i) + ".go")
	}
	// Read into dir B resets consecutive counter for A.
	s.recordRead("dirB/other.go")
	// Now 3 reads into dir A should not trigger (counter restarted at 1).
	for i := 4; i <= 6; i++ {
		hint := s.recordRead("dirA/f" + strconv.Itoa(i) + ".go")
		if hint != "" {
			t.Fatalf("read %d after patch switch should not trigger, got: %s", i, hint)
		}
	}
}

func TestPatchExhaust_MaxFires(t *testing.T) {
	s := newPatchExhaustState()
	dir := "pkg/bar"

	// Trigger first fire at read 4.
	s.recordRead(dir + "/a.go")
	s.recordRead(dir + "/b.go")
	s.recordRead(dir + "/c.go")
	h1 := s.recordRead(dir + "/d.go")
	if h1 == "" {
		t.Fatal("expected first hint at read 4")
	}

	// Continue reading - second fire should occur.
	h2 := s.recordRead(dir + "/e.go")
	if h2 == "" {
		t.Fatal("expected second hint at read 5")
	}

	// After 2 fires, no more hints.
	for i := 6; i <= 10; i++ {
		h := s.recordRead(dir + "/f" + strconv.Itoa(i) + ".go")
		if h != "" {
			t.Fatalf("read %d should not trigger (max fires reached), got: %s", i, h)
		}
	}
}

func TestPatchExhaust_Reset(t *testing.T) {
	s := newPatchExhaustState()
	s.recordRead("pkg/x/a.go")
	s.recordRead("pkg/x/b.go")
	s.recordRead("pkg/x/c.go")
	s.recordRead("pkg/x/d.go") // triggers
	if s.fires != 1 {
		t.Fatalf("expected 1 fire before reset, got %d", s.fires)
	}

	s.reset()
	if s.fires != 0 || s.consecutiveCount != 0 || len(s.patchReadCounts) != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestPatchOf(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"internal/agent/foo.go", "internal/agent"},
		{"/abs/path/to/file.go", "/abs/path/to"},
		{"root.go", "."},
		{"", ""},
		{"dir/sub/", "dir/sub"}, // trailing slash cleaned
	}
	for _, c := range cases {
		got := patchOf(c.input)
		if got != c.want {
			t.Errorf("patchOf(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
