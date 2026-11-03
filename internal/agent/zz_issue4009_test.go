package agent

import "testing"

func issue4009MultiEditArgs() []byte {
	return []byte(`{"files":[{"path":"internal/agent/a.go","edits":[{"old_text":"x","new_text":"y"}]},{"path":"internal/tool/b.go","edits":[{"old_text":"x","new_text":"y"}]}]}`)
}

func issue4009WrittenContent() string {

	return `{"written":2,"failed":0,"skipped":0,"written_paths":["internal/agent/a.go","internal/tool/b.go"]}`
}

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

func TestIssue4009VerificationSingleShotOutcomeBlind(t *testing.T) {
	a := &Agent{driftRecurrence: newDriftRecurrenceState()}
	a.driftRecurrenceMarkWarn(1)
	args := `{"command":"go test ./..."}`

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
