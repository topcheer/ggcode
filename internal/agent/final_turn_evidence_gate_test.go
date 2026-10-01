package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// r357: the agent edited source this run, ran NO build/test command, and a
// build system exists -> the stop must be gated with a concrete receipt
// command naming the edited file's package.
func TestFinalTurnEvidenceGate_BlocksEditsWithoutVerify(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir)
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0o644)

	msg := finalTurnEvidenceGate(2, src, false, false, dir)
	if msg == "" || !strings.Contains(msg, "go test") {
		t.Fatalf("want gate with targeted go test command, got %q", msg)
	}
	if !strings.Contains(msg, "2 source file") {
		t.Fatalf("message should mention edit count, got %q", msg)
	}
}

// Already verified this run -> stop is allowed (receipt exists).
func TestFinalTurnEvidenceGate_VerifiedRunAllowed(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir)
	if msg := finalTurnEvidenceGate(2, filepath.Join(dir, "main.go"), true, false, dir); msg != "" {
		t.Fatalf("verified run must pass, got %q", msg)
	}
}

// No edits -> nothing to gate (docs-only or pure-answer runs).
func TestFinalTurnEvidenceGate_NoEditsAllowed(t *testing.T) {
	if msg := finalTurnEvidenceGate(0, "", false, false, t.TempDir()); msg != "" {
		t.Fatalf("no edits must pass, got %q", msg)
	}
}

// No build system -> no canonical receipt to demand.
func TestFinalTurnEvidenceGate_NoBuildSystemAllowed(t *testing.T) {
	dir := t.TempDir()
	if msg := finalTurnEvidenceGate(3, filepath.Join(dir, "x.go"), false, false, dir); msg != "" {
		t.Fatalf("no build system must pass, got %q", msg)
	}
}

// The gate fires at most once per run - the second stop is allowed.
func TestFinalTurnEvidenceGate_FiresOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir)
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0o644)

	if msg := finalTurnEvidenceGate(1, src, false, false, dir); msg == "" {
		t.Fatal("first stop should be gated")
	}
	if msg := finalTurnEvidenceGate(1, src, false, true, dir); msg != "" {
		t.Fatalf("second stop must be allowed, got %q", msg)
	}
}

func writeGoModule(t *testing.T, dir string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.27\n"), 0o644)
}
