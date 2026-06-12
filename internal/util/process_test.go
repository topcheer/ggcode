package util

import (
	"os"
	"testing"
)

func TestIsProcessAlive_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	if !IsProcessAlive(pid) {
		t.Errorf("IsProcessAlive(%d) = false, want true (current process)", pid)
	}
}

func TestIsProcessAlive_NonExistent(t *testing.T) {
	// Use a very high PID that's unlikely to exist
	if IsProcessAlive(999999999) {
		t.Error("IsProcessAlive(999999999) = true, want false")
	}
}

func TestIsProcessAlive_InvalidPID(t *testing.T) {
	if IsProcessAlive(0) {
		t.Error("IsProcessAlive(0) = true, want false")
	}
	if IsProcessAlive(-1) {
		t.Error("IsProcessAlive(-1) = true, want false")
	}
}

func TestIsProcessAliveProc_CurrentProcess(t *testing.T) {
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Skipf("FindProcess failed: %v", err)
	}
	if !IsProcessAliveProc(proc) {
		t.Error("IsProcessAliveProc(current) = false, want true")
	}
}

func TestIsProcessAliveProc_Nil(t *testing.T) {
	if IsProcessAliveProc(nil) {
		t.Error("IsProcessAliveProc(nil) = true, want false")
	}
}
