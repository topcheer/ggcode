package agent

import (
	"fmt"
	"testing"
)

func TestTunnelVision_NoWarningBeforeMinIterations(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a/main.go")
	s.recordFile("/b/util.go")

	if msg := s.check(3); msg != "" {
		t.Errorf("expected no warning before min iterations, got: %s", msg)
	}
}

func TestTunnelVision_NoWarningWithEnoughFiles(t *testing.T) {
	s := newTunnelVisionState()
	for _, p := range []string{"/a.go", "/b.go", "/c.go", "/d.go", "/e.go", "/f.go"} {
		s.recordFile(p)
	}

	if msg := s.check(20); msg != "" {
		t.Errorf("expected no warning with enough files, got: %s", msg)
	}
}

func TestTunnelVision_WarnsOnNarrowScope(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/project/main.go")
	s.recordFile("/project/util.go")

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

	first := s.check(8)
	if first == "" {
		t.Fatal("expected warning on first check")
	}

	second := s.check(12)
	if second != "" {
		t.Errorf("expected no duplicate warning, got: %s", second)
	}
}

func TestTunnelVision_RatioBelowThreshold(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.recordFile("/b.go")

	if msg := s.check(6); msg != "" {
		t.Errorf("expected no warning with ratio below threshold, got: %s", msg)
	}
}

func TestTunnelVision_ExcludesTestAndMdFiles(t *testing.T) {

	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.recordFile("/a_test.go")
	s.recordFile("/README.md")
	if msg := s.check(10); msg != "" {
		t.Fatalf("expected NO warning for a test-fix task (test file touched), got: %s", msg)
	}

	s2 := newTunnelVisionState()
	s2.recordFile("/a.go")
	s2.recordFile("/README.md")
	if msg := s2.check(10); msg == "" {
		t.Fatal("expected warning when md excluded and ratio high")
	}
}

func TestTunnelVision_SearchBreadthCounts(t *testing.T) {

	s := newTunnelVisionState()
	s.recordFile("/internal/agent/agent.go")
	s.recordFile("/internal/tool/cli.go")
	for i := 0; i < 12; i++ {
		s.recordSearched(fmt.Sprintf("/internal/pkg%d/file.go", i))
	}
	if msg := s.check(10); msg != "" {
		t.Fatalf("expected no warning for search-broad exploration, got: %s", msg)
	}

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
	s.check(10)
	s.reset()

	s.recordFile("/a.go")
	msg := s.check(10)
	if msg == "" {
		t.Fatal("expected warning after reset with 1 file and 10 iterations")
	}
}

func TestTunnelVision_EmptyPathIgnored(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("")

	if msg := s.check(10); msg != "" {
		t.Errorf("expected no warning with no files tracked, got: %s", msg)
	}
}

func TestTunnelVision_OverlapNotDoubleCounted(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.recordFile("/b.go")
	s.recordFile("/c.go")

	for _, p := range []string{"/a.go", "/b.go", "/c.go"} {
		s.recordSearched(p)
	}

	if msg := s.check(16); msg == "" {
		t.Fatal("expected warning: overlapped read+search files must not double-count toward breadth (union=3 < 5)")
	}
}

func TestTunnelVision_DisjointSearchStillSuppresses(t *testing.T) {
	s := newTunnelVisionState()
	s.recordFile("/a.go")
	s.recordFile("/b.go")
	s.recordFile("/c.go")
	for _, p := range []string{"/d.go", "/e.go"} {
		s.recordSearched(p)
	}

	if msg := s.check(16); msg != "" {
		t.Fatalf("expected no warning with union=5, got: %s", msg)
	}
}

func TestTunnelVision_PathAnchoringMergesKeys(t *testing.T) {
	s := newTunnelVisionState()
	s.baseDir = "/repo"
	s.recordFile("/repo/internal/a.go")
	s.recordSearched("internal/a.go")
	if len(s.searchedFiles) != 1 || len(s.filesTouched) != 1 {
		t.Fatalf("expected 1+1 keys after anchoring, got %d+%d", len(s.filesTouched), len(s.searchedFiles))
	}
	for k := range s.searchedFiles {
		if _, ok := s.filesTouched[k]; !ok {
			t.Fatalf("searched key %q not merged with filesTouched key", k)
		}
	}

	if msg := s.check(10); msg == "" {
		t.Fatal("expected warning with anchored single file and ratio 10")
	}
}
