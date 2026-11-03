package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withFreshTracker(t *testing.T) {
	t.Helper()
	orig := defaultFileTracker
	defaultFileTracker = NewFileIntegrityTracker()
	t.Cleanup(func() { defaultFileTracker = orig })
}

func TestMultiFileRead_RecordsOnlySeenFiles(t *testing.T) {
	withFreshTracker(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.txt")
	missing := filepath.Join(dir, "missing.txt")
	blocked := filepath.Join(dir, "blocked.txt")
	for _, p := range []string{good, blocked} {
		if err := os.WriteFile(p, []byte("line1\nline2\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": good},
			{"path": missing},
		},
	})
	res, err := MultiFileRead{}.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("unexpected failure: %v %s", err, res.Content)
	}

	if !defaultFileTracker.HasBeenSeen(good) {
		t.Error("successfully read file should be tracked (HasBeenSeen=true)")
	}
	if defaultFileTracker.HasBeenSeen(missing) {
		t.Error("file that failed to read (ERROR block) must NOT be tracked")
	}

	origTracker := defaultFileTracker
	defaultFileTracker = NewFileIntegrityTracker()
	defer func() { defaultFileTracker = origTracker }()
	input2, _ := json.Marshal(map[string]any{
		"files": []map[string]any{{"path": blocked}},
	})
	res2, err := MultiFileRead{SandboxCheck: func(p string) bool { return false }}.Execute(context.Background(), input2)
	if err != nil || res2.IsError {
		t.Fatalf("unexpected failure: %v %s", err, res2.Content)
	}
	if !strings.Contains(res2.Content, "=== ERROR: ") {
		t.Fatalf("expected ERROR block for sandbox-denied file, got: %s", res2.Content)
	}
	if defaultFileTracker.HasBeenSeen(blocked) {
		t.Error("sandbox-denied file must NOT be tracked")
	}
}

func TestMultiFileRead_SkippedFileNotTracked(t *testing.T) {
	withFreshTracker(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("aaa\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("bbb\n"), 0644); err != nil {
		t.Fatal(err)
	}

	big := filepath.Join(dir, "big.txt")
	var sb strings.Builder
	longLine := strings.Repeat("x", maxMultiFileReadTotalBytes/4)
	for i := 0; i < 5; i++ {
		sb.WriteString(longLine)
		sb.WriteString("\n")
	}
	if err := os.WriteFile(big, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	_ = a

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": big},
			{"path": b},
		},
	})
	res, err := MultiFileRead{}.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("unexpected failure: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "skipped=2") {
		t.Fatalf("expected both files skipped (big exceeds budget), got: %s", res.Content)
	}

	if defaultFileTracker.HasBeenSeen(b) {
		t.Error("skipped file must NOT be tracked")
	}
	if defaultFileTracker.HasBeenSeen(big) {
		t.Error("oversized file whose block was dropped must NOT be tracked")
	}
}

func TestMultiFileEdit_AtomicRecordsWrite(t *testing.T) {
	withFreshTracker(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc f() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"mode": "atomic",
		"files": []map[string]any{
			{
				"path": target,
				"edits": []map[string]any{
					{"old_text": "func f() {}", "new_text": "func g() {}"},
				},
			},
		},
	})
	res, err := MultiFileEdit{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var out MultiFileEditContent
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("invalid result JSON: %v\n%s", err, res.Content)
	}
	if !out.Applied || out.WrittenFiles != 1 {
		t.Fatalf("expected atomic success, got: %s", res.Content)
	}

	if !defaultFileTracker.HasBeenSeen(target) {
		t.Fatal("atomic multi_file_edit should record the write")
	}
	stale, _ := defaultFileTracker.CheckStale(target)
	if stale {
		t.Error("file edited by atomic multi_file_edit must not be reported stale (missing RecordWrite)")
	}

	data, _ := os.ReadFile(target)
	if err := os.WriteFile(target, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestMultiFileEdit_DiagnosticBaselineClearedOnRollback(t *testing.T) {
	diagBaselineMu.Lock()
	saved := diagBaselines
	diagBaselines = map[string]baselineSnapshot{}
	diagBaselineMu.Unlock()
	defer func() {
		diagBaselineMu.Lock()
		diagBaselines = saved
		diagBaselineMu.Unlock()
	}()

	dir := t.TempDir()
	okFile := filepath.Join(dir, "ok.go")
	if err := os.WriteFile(okFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	badDir := filepath.Join(dir, "badpath")
	if err := os.Mkdir(badDir, 0755); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"mode": "atomic",
		"files": []map[string]any{
			{"path": okFile, "edits": []map[string]any{
				{"old_text": "package main", "new_text": "package main\n// edited"},
			}},
			{"path": badDir, "edits": []map[string]any{
				{"old_text": "package main", "new_text": "package other"},
			}},
		},
	})
	res, err := MultiFileEdit{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected atomic failure, got: %s", res.Content)
	}

	diagBaselineMu.Lock()
	n := len(diagBaselines)
	diagBaselineMu.Unlock()
	if n != 0 {
		t.Errorf("expected diagnostic baselines to be cleared after rollback, found %d", n)
	}

	data, _ := os.ReadFile(okFile)
	if strings.Contains(string(data), "edited") {
		t.Error("atomic mode should roll back already-written files on failure")
	}
}
