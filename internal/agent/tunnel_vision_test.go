package agent

import "testing"

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
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.recordFile("/a_test.go")
	s.recordFile("/README.md")
	// Only /a.go counts as a real file
	// 10 iterations, 1 file -> ratio 10.0
	msg := s.check(10)
	if msg == "" {
		t.Fatal("expected warning: test/md files excluded from breadth count")
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
