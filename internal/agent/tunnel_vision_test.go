package agent

import (
	"fmt"
	"testing"
)

func TestTunnelVision_NoWarningBeforeMinIterations(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a/main.go")
	s.recordFile("/b/util.go")
	// Only 3 iterations, below tvMinIterations=8
	if msg := s.check(3); msg != "" {
		t.Errorf("expected no warning before min iterations, got: %s", msg)
	}
}

func TestTunnelVision_NoWarningWithEnoughFiles(t *testing.T) {
	s := newTunnelVisionState()
	for _, p := range []string{"/a.go", "/b.go", "/c.go", "/d.go", "/e.go", "/f.go"} {
		s.recordFile(p)
	}
	// 6 files >= tvMinFilesForWarning=5, so no tunnel vision even with many iterations
	if msg := s.check(20); msg != "" {
		t.Errorf("expected no warning with enough files, got: %s", msg)
	}
}

func TestTunnelVision_WarnsOnNarrowScope(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/project/main.go")
	s.recordFile("/project/util.go")
	// 10 iterations, 2 files -> ratio 5.0 >= 4.0 and files < 5 -> should warn
	msg := s.check(10)
	if msg == "" {
		t.Fatal("expected tunnel vision warning for 10 iterations / 2 files")
	}
	if !contains(msg, "10") {
		t.Errorf("warning should mention iteration count: %s", msg)
	}
	if !contains(msg, "2") {
		t.Errorf("warning should mention file count: %s", msg)
	}
}

func TestTunnelVision_FiresOncePerRun(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	// 8 iterations, 1 file -> ratio 8.0
	first := s.check(8)
	if first == "" {
		t.Fatal("expected warning on first check")
	}
	// Should not fire again
	second := s.check(12)
	if second != "" {
		t.Errorf("expected no duplicate warning, got: %s", second)
	}
}

func TestTunnelVision_RatioBelowThreshold(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.recordFile("/b.go")
	// 6 iterations, 2 files -> ratio 3.0 < 4.0 threshold
	if msg := s.check(6); msg != "" {
		t.Errorf("expected no warning with ratio below threshold, got: %s", msg)
	}
}

func TestTunnelVision_ExcludesTestAndMdFiles(t *testing.T) {
	// _test.go / .md files stay excluded from the breadth COUNT (#476
	// keeps the exclusion), but TOUCHING a _test.go marks this as a
	// test-fix task — exempt from the ratio warning (scenario B).
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.recordFile("/a_test.go")
	s.recordFile("/README.md")
	if msg := s.check(10); msg != "" {
		t.Fatalf("expected NO warning for a test-fix task (test file touched), got: %s", msg)
	}

	// Without any test file: md-only exclusion still leaves 1 real file,
	// high ratio fires.
	s2 := newTunnelVisionState()
	s2.recordFile("/a.go")
	s2.recordFile("/README.md")
	if msg := s2.check(10); msg == "" {
		t.Fatal("expected warning when md excluded and ratio high")
	}
}

func TestTunnelVision_SearchBreadthCounts(t *testing.T) {
	// #476 scenario A: 2 read files + 12 grep-seen files = broad
	// exploration — no "broaden exploration" misguidance.
	s := newTunnelVisionState()
	s.recordFile("/internal/agent/agent.go")
	s.recordFile("/internal/tool/cli.go")
	for i := 0; i < 12; i++ {
		s.recordSearched(fmt.Sprintf("/internal/pkg%d/file.go", i))
	}
	if msg := s.check(10); msg != "" {
		t.Fatalf("expected no warning for search-broad exploration, got: %s", msg)
	}

	// Narrow: only 2 files seen, no search breadth — still warns.
	s2 := newTunnelVisionState()
	s2.recordFile("/internal/agent/agent.go")
	s2.recordFile("/internal/tool/cli.go")
	if msg := s2.check(10); msg == "" {
		t.Fatal("expected warning for genuinely narrow exploration")
	}
}

func TestTunnelVision_ResetClearsState(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.check(10) // triggers warning
	s.reset()
	// After reset, re-record a file and verify warning fires again
	s.recordFile("/a.go")
	msg := s.check(10)
	if msg == "" {
		t.Fatal("expected warning after reset with 1 file and 10 iterations")
	}
}

func TestTunnelVision_EmptyPathIgnored(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("")
	// No real files tracked, so check should return empty
	if msg := s.check(10); msg != "" {
		t.Errorf("expected no warning with no files tracked, got: %s", msg)
	}
}
