package agent

// #3796 companion: background verification (start_command) must count as
// evidence for the final-turn gate, and the gate must fall back to the
// detected build-system command when no language-specific command exists.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3796_StartCommandCountsAsVerification(t *testing.T) {
	a := &Agent{} // maybeResetVerifyOnCommand only needs mu + postEditVerify
	a.postEditVerify.sourceEditsSinceHint = 3
	// Background test launch: start_command result is the spawn result.
	args := json.RawMessage(`{"command":"go test ./..."}`)
	a.maybeResetVerifyOnCommand("start_command", args, false)
	// #3840: the launch alone is soft evidence only - the spawn succeeding
	// says nothing about the test outcome, so realBuildOrTestRunThisRun and
	// lastBuildFailed must stay untouched until the terminal backfill.
	if a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("#3840: launch-only result must not count as real test evidence")
	}
	if a.postEditVerify.lastBuildFailed {
		t.Fatal("launch must not set the failure flag")
	}
	if !a.postEditVerify.buildOrTestRunThisRun {
		t.Fatal("launch still counts as build/test activity (#3796 soft evidence)")
	}
	if a.postEditVerify.sourceEditsSinceHint != 0 {
		t.Fatal("verification must reset the edit counter")
	}
	// The terminal outcome is the real evidence: a passed background test
	// now satisfies the final-turn gate's demand (#3796 intent, corrected).
	a.recordBgVerifyOutcome("go test ./...", true)
	if !a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("terminal PASS of a background test must set realBuildOrTestRunThisRun")
	}
	if a.postEditVerify.lastBuildFailed {
		t.Fatal("terminal PASS must clear the failure flag")
	}
}

func TestIssue3796_GateFallsBackToBuildSystemCommand(t *testing.T) {
	dir := t.TempDir()
	// A Makefile gives detectBuildSystem something to find, while the edited
	// file uses an extension with no langProfileForFile entry (.sh), so
	// targetedVerifyCommand returns "" - the old gate silently waved it through.
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("test:\n\techo ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := finalTurnEvidenceGate(2, filepath.Join(dir, "run.sh"), false, false, dir)
	if msg == "" {
		t.Fatal("gate must fall back to the build-system command instead of allowing the stop")
	}
	// Real verification still allows the stop.
	if got := finalTurnEvidenceGate(2, filepath.Join(dir, "run.sh"), true, false, dir); got != "" {
		t.Fatalf("verified run must pass the gate, got %q", got)
	}
	// Gate fires once.
	if got := finalTurnEvidenceGate(2, filepath.Join(dir, "run.sh"), false, true, dir); got != "" {
		t.Fatalf("already-fired gate must stay silent, got %q", got)
	}
}
