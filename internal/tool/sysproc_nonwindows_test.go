//go:build !windows

package tool

// sysproc_nonwindows.go is a deliberate no-op on Unix (process-group handling
// lives in configureCommandCancellation). The test pins the contract: calling
// it must not panic or mutate the command (sa-141).

import (
	"os/exec"
	"testing"
)

func TestApplyCreateNoWindowNoopSa141(t *testing.T) {
	cmd := exec.Command("true")
	applyCreateNoWindow(cmd)
	if cmd.Path == "" {
		t.Fatal("applyCreateNoWindow mutated cmd.Path")
	}
}
