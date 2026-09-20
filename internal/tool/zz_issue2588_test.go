//go:build unix

package tool

import (
	"os/exec"
	"strings"
	"testing"
)

// TestIssue2588_SignalKillProducesExitCodeDiagnostics pins #2588: when a
// process is killed by a signal, exec.ExitError.ExitCode() returns -1
// (not 128+N), so both call sites fed interpretExitCode a value that
// tripped its `exitCode <= 1` early return -- every 128+N signal
// diagnostic (137 OOM / 139 segfault / ...) was dead code exactly on
// the kills it exists to explain. exitCodeFromErr must convert the
// WaitStatus signal number to 128+N.
func TestIssue2588_SignalKillProducesExitCodeDiagnostics(t *testing.T) {
	// `sh -c 'kill -9 $$'`: the shell kills itself -> WaitStatus.Signaled()
	// with signal 9 -> exitCodeFromErr must report 137 (the OOM diagnostic).
	err := exec.Command("sh", "-c", "kill -9 $$").Run()
	if err == nil {
		t.Fatal("expected an error from self-kill")
	}
	code := exitCodeFromErr(err)
	if code != 137 {
		t.Fatalf("exitCodeFromErr(self SIGKILL) = %d, want 137", code)
	}
	intel := interpretExitCode(code)
	if !strings.Contains(intel, "137") || !strings.Contains(intel, "SIGKILL") {
		t.Fatalf("interpretExitCode(137) = %q, want SIGKILL/OOM diagnostic", intel)
	}

	// Normal (non-signal) exits keep their real code.
	err = exec.Command("sh", "-c", "exit 42").Run()
	if err == nil {
		t.Fatal("expected exit 42 to error")
	}
	if code := exitCodeFromErr(err); code != 42 {
		t.Fatalf("exitCodeFromErr(exit 42) = %d, want 42", code)
	}

	// Non-ExitError errors stay -1.
	if code := exitCodeFromErr(nil); code != -1 {
		t.Fatalf("exitCodeFromErr(nil) = %d, want -1", code)
	}
}

// SIGTERM (15) -> 143, exercising the second signal family (timeout
// process-group kills) through the same normalization.
func TestIssue2588_SigtermNormalizesTo143(t *testing.T) {
	err := exec.Command("sh", "-c", "kill -15 $$").Run()
	if err == nil {
		t.Fatal("expected an error from self-SIGTERM")
	}
	if code := exitCodeFromErr(err); code != 143 {
		t.Fatalf("exitCodeFromErr(self SIGTERM) = %d, want 143", code)
	}
}
