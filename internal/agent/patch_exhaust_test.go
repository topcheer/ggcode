package agent

import (
	"strconv"
	"strings"
	"testing"
)

func TestPatchExhaust_GiveUpThreshold(t *testing.T) {
	s := newPatchExhaustState()
	dir := "internal/agent"

	for i := 1; i <= 3; i++ {
		hint := s.recordRead(dir + "/file" + strconv.Itoa(i) + ".go")
		if hint != "" {
			t.Fatalf("read %d should not trigger hint, got: %s", i, hint)
		}
	}

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

	for i := 1; i <= 3; i++ {
		s.recordRead(dir + "/f" + strconv.Itoa(i) + ".go")
	}

	s.recordEdit(dir + "/f1.go")

	for i := 4; i <= 6; i++ {
		hint := s.recordRead(dir + "/f" + strconv.Itoa(i) + ".go")
		if hint != "" {
			t.Fatalf("read %d after edit should not trigger, got: %s", i, hint)
		}
	}
}

func TestPatchExhaust_SingleExcursionTolerance(t *testing.T) {
	s := newPatchExhaustState()

	for i := 1; i <= 3; i++ {
		if hint := s.recordRead("dirA/f" + strconv.Itoa(i) + ".go"); hint != "" {
			t.Fatalf("read %d should not trigger hint, got: %s", i, hint)
		}
	}

	if hint := s.recordRead("dirB/other.go"); hint != "" {
		t.Fatalf("excursion read should not trigger, got: %s", hint)
	}

	hint := s.recordRead("dirA/f4.go")
	if hint == "" {
		t.Fatal("expected excursion-tolerant hint on returning read (streak resumed)")
	}
}

func TestPatchExhaust_PingPongDoesNotAccumulate(t *testing.T) {
	s := newPatchExhaustState()

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

	for i := 1; i <= 2; i++ {
		s.recordRead("dirA/f" + strconv.Itoa(i) + ".go")
	}
	s.recordRead("dirB/x.go")
	s.recordRead("dirA/f3.go")
	s.recordRead("dirB/y.go")
	for i := 4; i <= 6; i++ {
		if hint := s.recordRead("dirA/f" + strconv.Itoa(i) + ".go"); hint != "" {
			t.Fatalf("read %d after hard reset should not trigger, got: %s", i, hint)
		}
	}
}

func TestPatchExhaust_MaxFires(t *testing.T) {
	s := newPatchExhaustState()
	dir := "pkg/bar"

	s.recordRead(dir + "/a.go")
	s.recordRead(dir + "/b.go")
	s.recordRead(dir + "/c.go")
	h1 := s.recordRead(dir + "/d.go")
	if h1 == "" {
		t.Fatal("expected first hint at read 4")
	}

	h2 := s.recordRead(dir + "/e.go")
	if h2 == "" {
		t.Fatal("expected second hint at read 5")
	}

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
	s.recordRead("pkg/x/d.go")
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
		{"./internal/agent/foo.go", "internal/agent"},
		{"/abs/path/to/file.go", "/abs/path/to"},
		{"root.go", "."},
		{"", ""},
		{"dir/sub/", "dir/sub"},
	}
	for _, c := range cases {
		got := patchOf(c.input)
		if got != c.want {
			t.Errorf("patchOf(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
