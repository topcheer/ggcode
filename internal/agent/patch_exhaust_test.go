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

func TestPatchExhaust_SingleExcursionTolerance(t *testing.T) {
	s := newPatchExhaustState()

	// 3 reads into dir A.
	for i := 1; i <= 3; i++ {
		if hint := s.recordRead("dirA/f" + strconv.Itoa(i) + ".go"); hint != "" {
			t.Fatalf("read %d should not trigger hint, got: %s", i, hint)
		}
	}
	// A SINGLE one-read excursion to dir B must NOT reset A's streak —
	// returning resumes the stashed count (#486: this tolerance was
	// documented from inception but never implemented).
	if hint := s.recordRead("dirB/other.go"); hint != "" {
		t.Fatalf("excursion read should not trigger, got: %s", hint)
	}
	// Returning to A: 3 stashed + 1 = 4 → fires.
	hint := s.recordRead("dirA/f4.go")
	if hint == "" {
		t.Fatal("expected excursion-tolerant hint on returning read (streak resumed)")
	}
}

func TestPatchExhaust_PingPongDoesNotAccumulate(t *testing.T) {
	s := newPatchExhaustState()

	// Alternating reads between two directories must never accumulate:
	// excursion tolerance is one-shot per streak, so leaving a resumed
	// streak hard-resets (#486 — reading source dir + test dir in
	// alternation is a legitimate workflow).
	for i := 0; i < 12; i++ {
		p := "dirA/f.go"
		if i%2 == 1 {
			p = "dirB/f.go"
		}
		if hint := s.recordRead(p); hint != "" {
			t.Fatalf("ping-pong read %d should not trigger, got: %s", i, hint)
		}
	}
}

func TestPatchExhaust_SecondDepartureAfterResumeResets(t *testing.T) {
	s := newPatchExhaustState()

	// A,A,B,A → A resumes (count 3). Then B again → resumed streak
	// hard-resets, so another 3 A reads stay below threshold.
	for i := 1; i <= 2; i++ {
		s.recordRead("dirA/f" + strconv.Itoa(i) + ".go")
	}
	s.recordRead("dirB/x.go")  // excursion
	s.recordRead("dirA/f3.go") // resume → count 3, no fire
	s.recordRead("dirB/y.go")  // second departure → hard reset
	for i := 4; i <= 6; i++ {
		if hint := s.recordRead("dirA/f" + strconv.Itoa(i) + ".go"); hint != "" {
			t.Fatalf("read %d after hard reset should not trigger, got: %s", i, hint)
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
		{"./internal/agent/foo.go", "internal/agent"}, // "./" prefix normalized away (#486)
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
