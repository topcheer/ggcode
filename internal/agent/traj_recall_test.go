package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedTrajRecall writes a trajectory-learnings.jsonl with a controlled mix of
// entries: a task-relevant one, an unrelated one, a stale one, and two entries
// sharing a category (only the newest of those should survive).
func seedTrajRecall(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := time.Now()
	old := now.Add(-45 * 24 * time.Hour)
	lines := []string{
		trajRecallJSON(t, trajRecallEntry{
			Timestamp: now.Add(-time.Hour),
			Type:      "optimization",
			Task:      "refactor the widget cache layer",
			Insight:   "Run completed with 20 iterations but only 1 edit - over-exploration pattern.",
			Category:  "over-exploration",
		}),
		trajRecallJSON(t, trajRecallEntry{
			Timestamp: now.Add(-2 * time.Hour),
			Type:      "optimization",
			Task:      "unrelated satellite telemetry task",
			Insight:   "Context pressure: peaked at 95% of window.",
			Category:  "context-pressure",
		}),
		trajRecallJSON(t, trajRecallEntry{
			Timestamp: old,
			Type:      "optimization",
			Task:      "ancient task",
			Insight:   "This stale insight should never appear.",
			Category:  "over-exploration",
		}),
		trajRecallJSON(t, trajRecallEntry{
			Timestamp: now.Add(-30 * time.Minute),
			Type:      "recovery",
			Task:      "fix flaky widget cache test",
			Insight:   "Recovered from 6 tool errors caused by stale imports.",
			Category:  "error-recovery",
		}),
		// Same category as the previous entry but NEWER: category dedup must
		// keep this one and drop the one above.
		trajRecallJSON(t, trajRecallEntry{
			Timestamp: now.Add(-10 * time.Minute),
			Type:      "recovery",
			Task:      "widget cache build fix",
			Insight:   "Recovered from compile errors by running go build before edits.",
			Category:  "error-recovery",
		}),
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
}

func trajRecallJSON(t *testing.T, e trajRecallEntry) string {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestTrajRecall_RetrievesRelevantDedupedFresh(t *testing.T) {
	dir := t.TempDir()
	seedTrajRecall(t, dir)
	a := &Agent{workingDir: dir}

	idx := a.recallTrajectoryInsights("refactor widget cache")
	if idx == "" {
		t.Fatalf("expected recall for matching task")
	}
	if !strings.Contains(idx, "over-exploration") {
		t.Fatalf("task-relevant insight missing: %q", idx)
	}
	if !strings.Contains(idx, "go build before edits") {
		t.Fatalf("newest same-category entry should win: %q", idx)
	}
	if strings.Contains(idx, "stale imports") {
		t.Fatalf("older same-category entry should be deduped away: %q", idx)
	}
	if strings.Contains(idx, "stale insight") {
		t.Fatalf("entry older than trajRecallMaxAge should be dropped: %q", idx)
	}
	if strings.Count(idx, "\n") > trajRecallMaxInsights-1 {
		t.Fatalf("more than %d insights injected: %q", trajRecallMaxInsights, idx)
	}
}

func TestTrajRecall_ColdStoresPayNothing(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{workingDir: dir}
	if got := a.recallTrajectoryInsights("anything"); got != "" {
		t.Fatalf("missing store should recall nothing, got %q", got)
	}

	// Malformed lines and empty insights are skipped, not fatal.
	path := filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("not json\n{\"insight\":\"\"}\n"), 0o644)
	if got := a.recallTrajectoryInsights("anything"); got != "" {
		t.Fatalf("all-invalid store should recall nothing, got %q", got)
	}
}

func TestTrajRecall_NoWorkingDirSafeNoop(t *testing.T) {
	if got := (&Agent{}).recallTrajectoryInsights("anything"); got != "" {
		t.Fatalf("missing workingDir should recall nothing, got %q", got)
	}
}
