package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormatPlaybookDigestEmpty(t *testing.T) {
	now := time.Now()
	got := FormatPlaybookDigest(nil, nil, now)
	if !strings.Contains(got, "no playbook data yet") {
		t.Errorf("expected playbook empty notice, got %q", got)
	}
	if !strings.Contains(got, "none yet") {
		t.Errorf("expected rules empty notice, got %q", got)
	}
}

func TestFormatPlaybookDigestOrdersByUses(t *testing.T) {
	now := time.Now()
	entries := []PlaybookEntry{
		{TaskType: "bugfix", ToolSequence: "read→edit→build", Uses: 3, SuccessRate: 1.0, AvgIter: 2.5, AvgDurationS: 90, LastSeen: now.Add(-1 * time.Hour)},
		{TaskType: "feature", ToolSequence: "read→edit", Uses: 12, SuccessRate: 0.83, AvgIter: 4.2, AvgDurationS: 312, LastSeen: now.Add(-2 * 24 * time.Hour), FileTypes: ".go"},
	}
	got := FormatPlaybookDigest(entries, nil, now)
	idxBug := strings.Index(got, "bugfix")
	idxFeat := strings.Index(got, "feature")
	if idxFeat == -1 || idxBug == -1 || idxFeat > idxBug {
		t.Fatalf("expected feature (uses=12) before bugfix (uses=3), got:\n%s", got)
	}
	if !strings.Contains(got, "success= 83%") {
		t.Errorf("expected success= 83%% rendered, got:\n%s", got)
	}
	if !strings.Contains(got, "2d ago") {
		t.Errorf("expected 2d ago age, got:\n%s", got)
	}
}

func TestFormatPlaybookDigestDeterministic(t *testing.T) {
	now := time.Now()
	rules := []Rule{
		{ID: "r1", Category: "test", Rule: "run go vet", LastSeen: now.Add(-10 * 24 * time.Hour), HitCount: 2},
		{ID: "r2", Category: "build", Rule: "use -tags goolm", LastSeen: now.Add(-45 * 24 * time.Hour), HitCount: 7},
	}
	a := FormatPlaybookDigest(nil, rules, now)
	b := FormatPlaybookDigest(nil, rules, now)
	if a != b {
		t.Fatal("digest must be deterministic for equal input")
	}
	if !strings.Contains(a, "2/60") {
		t.Errorf("expected count 2/60, got:\n%s", a)
	}
	if !strings.Contains(a, "1 stale") {
		t.Errorf("expected 1 stale rule (>30d), got:\n%s", a)
	}
	// The stalest-rules preview lists only stale rules; the fresh rule's
	// text must not appear.
	if !strings.Contains(a, "use -tags goolm") {
		t.Errorf("expected stale rule in preview, got:\n%s", a)
	}
	if strings.Contains(a, "run go vet") {
		t.Errorf("fresh rule must not appear in stale preview, got:\n%s", a)
	}
	if !strings.Contains(a, "hits=7, last=45d ago") {
		t.Errorf("expected stale rule detail line, got:\n%s", a)
	}
	if !strings.Contains(a, "build=1 test=1") {
		t.Errorf("expected category histogram, got:\n%s", a)
	}
}

func TestPlaybookSnapshotIsCopy(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)
	if pb == nil {
		t.Fatal("NewPlaybook returned nil")
	}
	entries := pb.Snapshot()
	entries = append(entries, PlaybookEntry{TaskType: "ghost"})
	if got := len(pb.Snapshot()); got != 0 {
		t.Fatalf("mutating snapshot leaked into store: %d entries", got)
	}
}

func TestPlaybookSnapshotLoadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	// Simulate a store persisted by a previous session.
	data := `[{"id":"e1","task_type":"bugfix","tool_sequence":"read→edit→build","uses":5,"success_rate":0.8,"avg_iter":3,"avg_duration_s":120,"last_seen":"2026-09-20T10:00:00Z","created_at":"2026-09-01T10:00:00Z"}]`
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "playbook.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := NewPlaybook(dir).Snapshot()
	if len(entries) != 1 || entries[0].TaskType != "bugfix" || entries[0].Uses != 5 {
		t.Fatalf("unexpected snapshot: %+v", entries)
	}
}
