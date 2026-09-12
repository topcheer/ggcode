package tool

// #2145 regression: multi_file_write returned a PLAIN-TEXT summary while
// the #2143 integrity loop (and the checkpoint unmarshal) probed the
// result for the MultiFileEditContent JSON shape - the unmarshal failed
// structurally for every multi_file_write result, so the per-file-outcome
// gate never activated and partial_success mode kept reporting false
// post-write mismatches for files the summary itself listed as failed.
// The tool now returns the JSON shape (human-readable lines in Summary).
// These tests drive the REAL tool output through the JSON probe, so a
// shape mismatch can never slip through synthetic literals again.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue2145_WriteResultCarriesWrittenPathsJSON(t *testing.T) {
	dir := t.TempDir()
	okFile := filepath.Join(dir, "ok.txt")

	tw := MultiFileWrite{WorkingDir: dir}
	res, err := tw.Execute(context.Background(), zz2145Marshal(t, map[string]interface{}{
		"files": []map[string]interface{}{
			{"path": okFile, "content": "hello"},
		},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	// The consumer-side probe (#2143): unmarshal written_paths.
	var wp struct {
		WrittenPaths []string `json:"written_paths"`
		FailedPaths  []string `json:"failed_paths"`
		Summary      string   `json:"summary"`
	}
	if err := json.Unmarshal([]byte(res.Content), &wp); err != nil {
		t.Fatalf("result must be the MultiFileEditContent JSON shape (the integrity loop and checkpoint unmarshal probe it): %v\ncontent: %s", err, res.Content)
	}
	if len(wp.WrittenPaths) != 1 || wp.WrittenPaths[0] != okFile {
		t.Fatalf("written_paths = %v, want [%s]", wp.WrittenPaths, okFile)
	}
	if !strings.Contains(wp.Summary, "✓") {
		t.Fatalf("human-readable lines must live in summary, got: %q", wp.Summary)
	}
}

// The partial_success failure shape: a denied file must land in
// failed_paths (the integrity loop skips exactly these).
func TestIssue2145_WritePartialFailureShape(t *testing.T) {
	dir := t.TempDir()
	okFile := filepath.Join(dir, "ok.txt")
	blocked := filepath.Join(dir, "blocked.txt")
	if err := os.WriteFile(blocked, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	tw := MultiFileWrite{
		WorkingDir: dir,
		// Deny the blocked file via the sandbox path checker.
		SandboxCheck: func(path string) bool {
			return !strings.Contains(path, "blocked")
		},
	}
	res, err := tw.Execute(context.Background(), zz2145Marshal(t, map[string]interface{}{
		"mode": "partial_success",
		"files": []map[string]interface{}{
			{"path": okFile, "content": "new"},
			{"path": blocked, "content": "new"},
		},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("partial_success with failures must not be IsError: %s", res.Content)
	}

	var wp struct {
		WrittenPaths []string `json:"written_paths"`
		FailedPaths  []string `json:"failed_paths"`
	}
	if err := json.Unmarshal([]byte(res.Content), &wp); err != nil {
		t.Fatalf("partial result must stay JSON: %v\ncontent: %s", err, res.Content)
	}
	if len(wp.WrittenPaths) != 1 || wp.WrittenPaths[0] != okFile {
		t.Fatalf("written_paths = %v, want only the allowed file", wp.WrittenPaths)
	}
	if len(wp.FailedPaths) != 1 || wp.FailedPaths[0] != blocked {
		t.Fatalf("failed_paths = %v, want the denied file (the integrity loop skips it)", wp.FailedPaths)
	}
}

func zz2145Marshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(raw)
}
