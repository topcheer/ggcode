package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuidancePromoter_New(t *testing.T) {
	gp := NewGuidancePromoter("", "s1")
	if gp != nil {
		t.Fatal("expected nil for empty workingDir")
	}
}

func TestGuidancePromoter_PromotionFlow(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, ".ggcode")
	os.MkdirAll(dir, 0755)

	// Simulate 3 sessions where "ANALYSIS-PARALYSIS" fires.
	for sessionN := 0; sessionN < 3; sessionN++ {
		gp := NewGuidancePromoter(dir, "session-"+string(rune('A'+sessionN)))
		gp.RecordTag("ANALYSIS-PARALYSIS")
		gp.RecordTag("ANALYSIS-PARALYSIS") // multiple fires in one session
		gp.RunEndHook()
	}

	// 4th session: should now have promoted reminder.
	gp4 := NewGuidancePromoter(dir, "session-D")
	hook := gp4.RunStartHook()
	if hook == "" {
		t.Fatal("expected proactive reminder after 3 sessions of recurrence")
	}
	if !strings.Contains(hook, "ANALYSIS-PARALYSIS") {
		t.Fatalf("expected hook to mention ANALYSIS-PARALYSIS, got: %s", hook)
	}
	if !strings.Contains(hook, "3 sessions") {
		t.Fatalf("expected hook to mention session count, got: %s", hook)
	}
}

func TestGuidancePromoter_NoPrematurePromotion(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, ".ggcode")
	os.MkdirAll(dir, 0755)

	// Only 2 sessions -- below threshold.
	for sessionN := 0; sessionN < 2; sessionN++ {
		gp := NewGuidancePromoter(dir, "s-"+string(rune('A'+sessionN)))
		gp.RecordTag("TOOL-STORM")
		gp.RunEndHook()
	}

	gp3 := NewGuidancePromoter(dir, "s-C")
	hook := gp3.RunStartHook()
	if hook != "" {
		t.Fatalf("expected no promotion below threshold, got: %s", hook)
	}
}

func TestGuidancePromoter_DistinctSessionsOnly(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, ".ggcode")
	os.MkdirAll(dir, 0755)

	// Same session ID fires many times -- should only count as 1 session.
	gp := NewGuidancePromoter(dir, "same-session")
	for i := 0; i < 10; i++ {
		gp.RecordTag("SILENT-ERROR")
	}
	gp.RunEndHook()

	// Another run with the same session ID.
	gp2 := NewGuidancePromoter(dir, "same-session")
	for i := 0; i < 10; i++ {
		gp2.RecordTag("SILENT-ERROR")
	}
	gp2.RunEndHook()

	// Check entry -- should have session_count=1, total_fires>1.
	gp3 := NewGuidancePromoter(dir, "check")
	gp3.load()
	entry, ok := gp3.entries["SILENT-ERROR"]
	if !ok {
		t.Fatal("expected SILENT-ERROR entry")
	}
	if entry.SessionCount != 1 {
		t.Fatalf("expected SessionCount=1, got %d", entry.SessionCount)
	}
	if entry.TotalFires != 2 {
		t.Fatalf("expected TotalFires=2 (once per run), got %d", entry.TotalFires)
	}
}

func TestGuidancePromoter_PersistAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, ".ggcode")
	os.MkdirAll(dir, 0755)

	gp1 := NewGuidancePromoter(dir, "s1")
	gp1.RecordTag("VERBOSE-DRIFT")
	gp1.RunEndHook()

	// New instance loads from disk.
	gp2 := NewGuidancePromoter(dir, "s2")
	gp2.load()
	if _, ok := gp2.entries["VERBOSE-DRIFT"]; !ok {
		t.Fatal("expected VERBOSE-DRIFT to persist across instances")
	}
}

func TestGuidancePromoter_Cap(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, ".ggcode")
	os.MkdirAll(dir, 0755)

	// Create more promoted tags than the cap.
	for n := 0; n < 8; n++ {
		tag := "TAG-" + string(rune('A'+n))
		for s := 0; s < 3; s++ {
			gp := NewGuidancePromoter(dir, "s-"+string(rune('A'+s))+"-"+string(rune('A'+n)))
			gp.RecordTag(tag)
			gp.RunEndHook()
		}
	}

	gp := NewGuidancePromoter(dir, "final")
	hook := gp.RunStartHook()
	// Count lines starting with "- [" in the hook.
	lines := 0
	for _, line := range strings.Split(hook, "\n") {
		if strings.HasPrefix(line, "- ") {
			lines++
		}
	}
	if lines > guidancePromoteMaxEntries {
		t.Fatalf("expected at most %d promoted entries, got %d", guidancePromoteMaxEntries, lines)
	}
}
