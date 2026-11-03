//go:build unix

package tool

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIssue2588_SignalKillProducesExitCodeDiagnostics(t *testing.T) {

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

	err = exec.Command("sh", "-c", "exit 42").Run()
	if err == nil {
		t.Fatal("expected exit 42 to error")
	}
	if code := exitCodeFromErr(err); code != 42 {
		t.Fatalf("exitCodeFromErr(exit 42) = %d, want 42", code)
	}

	if code := exitCodeFromErr(nil); code != -1 {
		t.Fatalf("exitCodeFromErr(nil) = %d, want -1", code)
	}
}

func TestIssue2588_SigtermNormalizesTo143(t *testing.T) {
	err := exec.Command("sh", "-c", "kill -15 $$").Run()
	if err == nil {
		t.Fatal("expected an error from self-SIGTERM")
	}
	if code := exitCodeFromErr(err); code != 143 {
		t.Fatalf("exitCodeFromErr(self SIGTERM) = %d, want 143", code)
	}
}
