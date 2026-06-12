package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGitReadOnlyDescriptionsClarifyInspectionUse(t *testing.T) {
	cases := []struct {
		name string
		desc string
		want []string
	}{
		{"git_log", GitLog{}.Description(), []string{"Read-only inspection", "recent changes", "before editing"}},
		{"git_branch_list", GitBranchList{}.Description(), []string{"Read-only inspection", "remote=true", "expected branch"}},
		{"git_remote", GitRemote{}.Description(), []string{"Read-only inspection", "fetch/push", "upstream repository"}},
	}
	for _, tc := range cases {
		for _, want := range tc.want {
			if !strings.Contains(tc.desc, want) {
				t.Fatalf("%s description should mention %q, got %q", tc.name, want, tc.desc)
			}
		}
	}
}

func TestGitDiffDescriptionRemindsInspection(t *testing.T) {
	desc := GitDiff{}.Description()
	for _, want := range []string{"inspect exactly what changed", "before staging or committing", "specific file diffs"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("git_diff description should mention %q, got %q", want, desc)
		}
	}
}

func TestGitStatusBasic(t *testing.T) {
	gs := GitStatus{}
	// Running in the project repo — should work
	input, _ := json.Marshal(map[string]string{"path": "."})
	result, err := gs.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic — may be clean or have changes
	t.Logf("git status: %s", result.Content)
}

func TestGitStatusInvalidInput(t *testing.T) {
	gs := GitStatus{}
	result, err := gs.Execute(context.Background(), json.RawMessage(`bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid input")
	}
}

func TestGitStatusNonexistentPath(t *testing.T) {
	gs := GitStatus{}
	input, _ := json.Marshal(map[string]string{"path": "/nonexistent/path"})
	result, err := gs.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent path")
	}
}

func TestGitDiffBasic(t *testing.T) {
	gd := GitDiff{}
	input, _ := json.Marshal(map[string]string{"path": "."})
	result, err := gd.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic — may be clean or have diffs
	t.Logf("git diff: %d chars", len(result.Content))
}

func TestGitDiffInvalidInput(t *testing.T) {
	gd := GitDiff{}
	result, err := gd.Execute(context.Background(), json.RawMessage(`bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid input")
	}
}

func TestGitDiffCached(t *testing.T) {
	gd := GitDiff{}
	input, _ := json.Marshal(map[string]interface{}{"path": ".", "cached": true})
	result, err := gd.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	t.Logf("git diff --cached: %d chars", len(result.Content))
}

func TestGitLogBasic(t *testing.T) {
	gl := GitLog{}
	input, _ := json.Marshal(map[string]interface{}{"path": ".", "count": 5})
	result, err := gl.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	t.Logf("git log: %s", result.Content)
}

func TestGitLogInvalidInput(t *testing.T) {
	gl := GitLog{}
	result, err := gl.Execute(context.Background(), json.RawMessage(`bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid input")
	}
}

func TestGitLogZeroCount(t *testing.T) {
	gl := GitLog{}
	input, _ := json.Marshal(map[string]interface{}{"path": ".", "count": 0})
	result, err := gl.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Zero count → defaults to 10
	t.Logf("git log zero count: %s", result.Content)
}

func TestGitLogNonexistentPath(t *testing.T) {
	gl := GitLog{}
	input, _ := json.Marshal(map[string]interface{}{"path": "/nonexistent", "count": 5})
	result, err := gl.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent path")
	}
}

func TestGitShowDescriptionIsLocalGit(t *testing.T) {
	gs := GitShow{}
	if !strings.Contains(gs.Description(), "local Git") {
		t.Fatalf("git_show description should describe local Git behavior, got %q", gs.Description())
	}
	if strings.Contains(gs.Description(), "GitHub repository") {
		t.Fatalf("git_show description should not imply GitHub API behavior, got %q", gs.Description())
	}
}

func TestGitToolDescriptionsDiscourageBroadOrDestructiveActions(t *testing.T) {
	ga := GitAdd{}
	if !strings.Contains(ga.Description(), "avoid git_add files=[\".\"]") {
		t.Fatalf("git_add description should discourage broad staging, got %q", ga.Description())
	}
	if !strings.Contains(string(ga.Parameters()), "Prefer explicit paths") {
		t.Fatalf("git_add schema should prefer explicit paths, got %s", string(ga.Parameters()))
	}

	gc := GitCommit{}
	if !strings.Contains(gc.Description(), "inspecting git status/diff") {
		t.Fatalf("git_commit description should require status/diff inspection, got %q", gc.Description())
	}
	if !strings.Contains(string(gc.Parameters()), "new untracked files are not added") {
		t.Fatalf("git_commit all schema should mention untracked files, got %s", string(gc.Parameters()))
	}

	gs := GitStash{}
	if !strings.Contains(gs.Description(), "pop and drop are destructive") {
		t.Fatalf("git_stash description should warn about destructive actions, got %q", gs.Description())
	}
	if !strings.Contains(gs.Description(), "tracked changes only") {
		t.Fatalf("git_stash description should mention tracked-only push behavior, got %q", gs.Description())
	}
	params := string(gs.Parameters())
	for _, want := range []string{"drop removes an entry without applying", "Confirm the desired entry with git_stash_list", "untracked files are not included"} {
		if !strings.Contains(params, want) {
			t.Fatalf("git_stash schema should mention %q, got %s", want, params)
		}
	}
}
