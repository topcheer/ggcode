package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCalibrationStore_RecordMergesByKey(t *testing.T) {
	cs := newCalibrationStore(t.TempDir())
	cs.record(calibKindPredSuccessGotFailure, "run_command", "this should pass")
	cs.record(calibKindPredSuccessGotFailure, "run_command", "build should succeed now")
	cs.record(calibKindPredContentGotEmpty, "read_file", "file should contain config")
	if got := len(cs.lessons); got != 2 {
		t.Fatalf("want 2 merged lessons, got %d", got)
	}
	l := cs.lessons[calibKindPredSuccessGotFailure+"|run_command"]
	if l.Count != 2 {
		t.Fatalf("want count 2, got %d", l.Count)
	}
	if l.Snippet != "build should succeed now" {
		t.Fatalf("want latest snippet kept, got %q", l.Snippet)
	}
}

func TestCalibrationStore_EvictsOldestBeyondCap(t *testing.T) {
	cs := newCalibrationStore(t.TempDir())
	cs.record(calibKindPredContentGotEmpty, "read_file", "oldest")
	cs.lessons[calibKindPredContentGotEmpty+"|read_file"].LastSeen = "2020-01-01T00:00:00Z"
	for i := 0; i < calibrationStoreMaxLessons; i++ {
		cs.record(calibKindPredSuccessGotFailure, "tool", "")
		// distinct tools to avoid merging
		cs.lessons[calibKindPredSuccessGotFailure+"|tool"].Tool = "tool" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		cs.lessons[calibKindPredSuccessGotFailure+"|tool"].Key = calibKindPredSuccessGotFailure + "|" + cs.lessons[calibKindPredSuccessGotFailure+"|tool"].Tool
		cs.lessons[cs.lessons[calibKindPredSuccessGotFailure+"|tool"].Key] = cs.lessons[calibKindPredSuccessGotFailure+"|tool"]
		delete(cs.lessons, calibKindPredSuccessGotFailure+"|tool")
	}
	if len(cs.lessons) > calibrationStoreMaxLessons {
		t.Fatalf("cap exceeded: %d", len(cs.lessons))
	}
	if _, ok := cs.lessons[calibKindPredContentGotEmpty+"|read_file"]; ok {
		t.Fatal("oldest lesson should have been evicted")
	}
}

func TestCalibrationStore_RecallEmptyAndNil(t *testing.T) {
	var nilStore *calibrationStore
	if got := nilStore.recall(3); got != "" {
		t.Fatalf("nil store recall should be empty, got %q", got)
	}
	cs := newCalibrationStore(t.TempDir())
	if got := cs.recall(3); got != "" {
		t.Fatalf("empty store recall should be empty, got %q", got)
	}
}

func TestCalibrationStore_RecallFormat(t *testing.T) {
	cs := newCalibrationStore(t.TempDir())
	cs.record(calibKindPredSuccessGotFailure, "run_command", "the test should pass")
	out := cs.recall(3)
	if !strings.Contains(out, "run_command") || !strings.Contains(out, "predicted_success_got_failure") || !strings.Contains(out, "the test should pass") {
		t.Fatalf("recall missing key fields: %q", out)
	}
	if !strings.HasPrefix(out, "- ") {
		t.Fatalf("recall should be a list block, got %q", out)
	}
}

func TestCalibrationStore_LoadCorruptJSONTolerated(t *testing.T) {
	dir := t.TempDir()
	cs := newCalibrationStore(dir)
	cs.record(calibKindPredSuccessGotFailure, "run_command", "s")
	if err := os.WriteFile(cs.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs2 := newCalibrationStore(dir)
	if got := cs2.recall(3); got != "" {
		t.Fatalf("corrupt file should yield empty store, got %q", got)
	}
}

func TestCalibrationStore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	cs := newCalibrationStore(dir)
	cs.record(calibKindPredFailureGotSuccess, "grep", "should fail")
	cs.record(calibKindPredFailureGotSuccess, "grep", "should fail again")
	cs2 := newCalibrationStore(dir)
	out := cs2.recall(3)
	if !strings.Contains(out, "grep") || !strings.Contains(out, "2 time(s)") {
		t.Fatalf("roundtrip lost data: %q", out)
	}
}

func TestForesightCalibrate_PersistCalledOnMismatch(t *testing.T) {
	var gotKind, gotTool, gotSnippet string
	s := &foresightCalibrateState{
		persist: func(kind, tool, snippet string) {
			gotKind, gotTool, gotSnippet = kind, tool, snippet
		},
	}
	s.recordPrediction("This should pass after my fix.", []provider.ToolCallDelta{{Name: "run_command"}}, 1)
	s.checkCalibration("run_command", "exit code 1", false, 2)
	if gotKind != calibKindPredSuccessGotFailure || gotTool != "run_command" || gotSnippet == "" {
		t.Fatalf("persist not called correctly: kind=%q tool=%q snippet=%q", gotKind, gotTool, gotSnippet)
	}
}

func TestForesightCalibrate_NoPersistOnMatch(t *testing.T) {
	called := false
	s := &foresightCalibrateState{persist: func(kind, tool, snippet string) { called = true }}
	s.recordPrediction("This should pass.", []provider.ToolCallDelta{{Name: "run_command"}}, 1)
	s.checkCalibration("run_command", "ok output", false, 2)
	if called {
		t.Fatal("persist must not fire when prediction matches observation")
	}
}

func TestCalibrationStore_NilSafeRecord(t *testing.T) {
	var cs *calibrationStore
	cs.record(calibKindPredSuccessGotFailure, "run_command", "x") // must not panic
}

func TestCalibrationStore_PathLayout(t *testing.T) {
	dir := t.TempDir()
	cs := newCalibrationStore(dir)
	cs.record(calibKindPredSuccessGotFailure, "run_command", "x")
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "memory", "calibration.json")); err != nil {
		t.Fatalf("calibration.json not persisted at expected path: %v", err)
	}
}
