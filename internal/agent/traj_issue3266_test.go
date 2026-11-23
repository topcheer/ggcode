package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The learnings store is panel-only now (no prompt injection), so the list
// view must still merge the global tier: a global entry whose category is
// not shadowed by a local one shows up flagged General.
func TestIssue3266_PanelMergesGlobalTier(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now().UTC()

	writeR460Store(t, filepath.Join(ws, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		{Category: "c1", Type: "strategy", Insight: "local-1", Confidence: 0.9, LastReinforced: now},
	})
	writeR460Store(t, filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		{Category: "gcat", Type: "pattern", Insight: "global-only", Confidence: 0.9, LastReinforced: now},
		{Category: "c1", Type: "pattern", Insight: "global-shadowed", Confidence: 0.9, LastReinforced: now},
	})
	views := TrajListLearnings(ws)
	var sawLocal, sawGeneral, sawShadowed bool
	for _, v := range views {
		switch v.Insight {
		case "local-1":
			sawLocal = true
			if v.General {
				t.Fatal("local entry must not be flagged General")
			}
		case "global-only (general, other projects)":
			sawGeneral = true
			if !v.General {
				t.Fatal("global-tier entry must be flagged General")
			}
		case "global-shadowed (general, other projects)":
			sawShadowed = true
		}
	}
	if !sawLocal || !sawGeneral {
		t.Fatalf("local=%v general=%v: both tiers must be visible", sawLocal, sawGeneral)
	}
	if sawShadowed {
		t.Fatal("global entry whose category a local entry covers must be hidden")
	}
}

func writeR460Store(t *testing.T, path string, entries []trajectoryLearning) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTrajFile(path, entries); err != nil {
		t.Fatal(err)
	}
}
