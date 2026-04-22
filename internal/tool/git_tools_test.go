package tool

import (
	"context"
	"encoding/json"
	"testing"
)

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
