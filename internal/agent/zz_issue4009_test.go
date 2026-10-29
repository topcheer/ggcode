package agent

// #4009: driftRecurrenceRecord was fed extractFileHint (single path), so a
// multi_file_edit whose non-first files hit a warned-about dir never counted
// as a recurrence and the escalation warning went unreported. The agent.go
// wiring now uses the #2992 case 3 form: extractFileHints + written_paths
// override + per-path record. These tests pin the scenario contract:
// - the old single-path feed misses the warned dir (the bug's mechanism),
// - the new multi-path feed catches it,
// - verification counting stays single-shot and outcome-blind (#1544 case B),
// - failed edits stay ok-gated (#1452-B).
// Mirror style follows checks_1544_test.go (drive the wrapper without a
// full Agent loop; the wiring block mirrors the scopeDrift siblings).

import "testing"

func issue4009MultiEditArgs() []byte {
	return []byte(`{"files":[{"path":"internal/agent/a.go","edits":[{"old_text":"x","new_text":"y"}]},{"path":"internal/tool/b.go","edits":[{"old_text":"x","new_text":"y"}]}]}`)
}

func issue4009WrittenContent() string {
	// extractWrittenPaths decodes leading JSON tokens; the tool-layer summary
	// body leads with the JSON object (file_batch_helpers.go WrittenPaths).
	return `{"written":2,"failed":0,"skipped":0,"written_paths":["internal/agent/a.go","internal/tool/b.go"]}`
}

// Pre-#4009 single-path feed: extractFileHint sees only files[0], so the
// drift-target dir (internal/tool, file #2) recurrence is invisible.
func TestIssue4009SinglePathFeedMissesWarnedDir(t *testing.T) {
	a := &Agent{driftRecurrence: newDriftRecurrenceState()}
	a.driftRecurrenceMarkWarn(3)
	args := issue4009MultiEditArgs()
	a.driftRecurrenceRecord("multi_file_edit", extractFileHint("multi_file_edit", args), string(args), true)
	if sig := dirSignature("internal/tool"); a.driftRecurrence.postWarnDirs[sig] {
		t.Fatalf("unexpected: single-path feed recorded the drift-target dir")
	}
	if len(a.driftRecurrence.postWarnDirs) != 1 || !a.driftRecurrence.postWarnDirs[dirSignature("internal/agent")] {
		t.Fatalf("single-path feed must record exactly the first file's dir: %v", a.driftRecurrence.postWarnDirs)
	}
	if a.driftRecurrence.postWarnEdits != 1 {
		t.Fatalf("postWarnEdits = %d, want 1", a.driftRecurrence.postWarnEdits)
	}
}

// #4009 wiring form: extractFileHints + written_paths override + per-path
// record - the warned-dir recurrence becomes visible and counted.
func TestIssue4009MultiPathFeedRecordsWarnedDir(t *testing.T) {
	a := &Agent{driftRecurrence: newDriftRecurrenceState()}
	a.driftRecurrenceMarkWarn(3)
	args := issue4009MultiEditArgs()
	paths := extractFileHints("multi_file_edit", args)
	if len(paths) != 1 || paths[0] != "internal/agent/a.go" {
		t.Fatalf("quick-scan anchor drifted (multi_file_edit should surface only files[0]): %v", paths)
	}
	if written := extractWrittenPaths(issue4009WrittenContent()); len(written) != 2 {
		t.Fatalf("written_paths override data unavailable: %v", written)
	} else {
		paths = written
	}
	for _, p := range paths {
		a.driftRecurrenceRecord("multi_file_edit", p, string(args), true)
	}
	if sig := dirSignature("internal/tool"); !a.driftRecurrence.postWarnDirs[sig] {
		t.Fatalf("warned-dir recurrence still missed after multi-path feed")
	}
	if a.driftRecurrence.postWarnEdits != 2 {
		t.Fatalf("postWarnEdits = %d, want 2 (both written files)", a.driftRecurrence.postWarnEdits)
	}
}

// The verification side is path-independent and must stay single-shot and
// outcome-blind: a red `go test` (ok=false) still counts exactly once even
// though the wiring now loops over paths (#1544 case B regression guard;
// run_command is hint-free so the loop body runs exactly once via the
// empty-hints fallback).
func TestIssue4009VerificationSingleShotOutcomeBlind(t *testing.T) {
	a := &Agent{driftRecurrence: newDriftRecurrenceState()}
	a.driftRecurrenceMarkWarn(1)
	args := `{"command":"go test ./..."}`
	// Mirror the wiring: hints empty -> fallback single call.
	paths := extractFileHints("run_command", []byte(args))
	if len(paths) != 0 {
		t.Fatalf("run_command unexpectedly yielded file hints: %v", paths)
	}
	paths = []string{extractFileHint("run_command", []byte(args))}
	for _, p := range paths {
		a.driftRecurrenceRecord("run_command", p, args, false)
	}
	if a.driftRecurrence.postWarnVerifies != 1 {
		t.Fatalf("postWarnVerifies = %d, want 1 (red test is still a verification run)", a.driftRecurrence.postWarnVerifies)
	}
	if a.driftRecurrence.postWarnEdits != 0 {
		t.Fatalf("postWarnEdits = %d, want 0 (verification must not count as edit)", a.driftRecurrence.postWarnEdits)
	}
}

// Failed edits must stay gated even on the multi-path feed (#1452-B /
// #1491 pattern shared with the sibling trackers).
func TestIssue4009FailedMultiEditNotRecorded(t *testing.T) {
	a := &Agent{driftRecurrence: newDriftRecurrenceState()}
	a.driftRecurrenceMarkWarn(2)
	for _, p := range extractWrittenPaths(issue4009WrittenContent()) {
		a.driftRecurrenceRecord("multi_file_edit", p, "{}", false)
	}
	if a.driftRecurrence.postWarnEdits != 0 || len(a.driftRecurrence.postWarnDirs) != 0 {
		t.Fatalf("failed edit recorded: edits=%d dirs=%v", a.driftRecurrence.postWarnEdits, a.driftRecurrence.postWarnDirs)
	}
}
