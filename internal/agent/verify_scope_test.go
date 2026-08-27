package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestBuildVerifyOraclePromptScoping pins the diff-scoped verification
// contract: the oracle prompt must carry the changed-file list and explicit
// narrowness rules so the inner loop verifies what changed instead of running
// the full CI pipeline after every small edit.
func TestBuildVerifyOraclePromptScoping(t *testing.T) {
	prompt := buildVerifyOraclePrompt([]string{
		"internal/tool/grep.go",
		"internal/tool/grep_test.go",
	})

	for _, want := range []string{
		"verification oracle",
		"Files changed in this run",
		"internal/tool/grep.go",
		"internal/tool/grep_test.go",
		"NARROWEST",
		"Whole-repo commands",
		"verify-ci",
		"SKIP",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// TestBuildVerifyOraclePromptNoFiles keeps the no-diff path compatible: the
// oracle still gets the scoping rules but no empty file section.
func TestBuildVerifyOraclePromptNoFiles(t *testing.T) {
	prompt := buildVerifyOraclePrompt(nil)
	if strings.Contains(prompt, "Files changed in this run") {
		t.Error("nil files should not produce a changed-files section")
	}
	if !strings.Contains(prompt, "NARROWEST") {
		t.Error("scoping rules must always be present")
	}
}

// TestBuildVerifyOraclePromptCapsFileList ensures a huge change set is
// summarized by directory instead of flooding the oracle call.
func TestBuildVerifyOraclePromptCapsFileList(t *testing.T) {
	files := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		files = append(files, fmt.Sprintf("pkg/dir%d/file%02d.go", i%3, i))
	}
	prompt := buildVerifyOraclePrompt(files)

	if got := strings.Count(prompt, "  - pkg/"); got != 40 {
		t.Errorf("expected exactly 40 listed files, got %d", got)
	}
	if !strings.Contains(prompt, "10 more files under") {
		t.Errorf("expected overflow summary for 10 unlisted files")
	}
	for _, d := range []string{"pkg/dir0/", "pkg/dir1/", "pkg/dir2/"} {
		if !strings.Contains(prompt, d) {
			t.Errorf("overflow summary missing dir %q", d)
		}
	}
}

// TestPostLoopVerifyDisabledByDefault pins the off-by-default contract: the
// post-loop verification pass must NOT run unless explicitly opted in via
// verify.auto_after_run. The system prompt already mandates in-loop scoped
// verification; a second whole-pass at loop end duplicated it and its
// failure injection could loop the agent on phantom errors.
func TestPostLoopVerifyDisabledByDefault(t *testing.T) {
	a := NewAgent(nil, nil, "", 10)

	var progress []string
	a.SetVerifyCallbacks(func(s string) { progress = append(progress, s) }, nil)

	rs := &RunStats{FilesEdited: []string{"x/y.go"}}
	a.syncVerifyAndGate(context.Background(), rs, 0) // must be a silent no-op
	a.asyncVerify(context.Background(), rs)

	if len(progress) != 0 {
		t.Errorf("default-off must short-circuit before any progress, got: %v", progress)
	}
}

// TestPostLoopVerifyOptInRestoresBehavior: with SetAutoVerify(true) the pass
// engages (progress fires) even without a provider or build system - the
// oracle simply decides nothing and the run skips cleanly.
func TestPostLoopVerifyOptInRestoresBehavior(t *testing.T) {
	dir := t.TempDir()
	a := NewAgent(nil, nil, "", 10)
	a.SetWorkingDir(dir)
	a.SetAutoVerify(true)

	var progress []string
	a.SetVerifyCallbacks(func(s string) { progress = append(progress, s) }, nil)

	rs := &RunStats{FilesEdited: []string{"x/y.go"}, ToolCalls: map[string]int{"edit_file": 1}}
	a.syncVerifyAndGate(context.Background(), rs, 0)
	a.asyncVerify(context.Background(), rs)

	if len(progress) == 0 {
		t.Error("opt-in must re-engage post-loop verification")
	}
}
