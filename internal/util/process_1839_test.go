//go:build !windows

package util

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// #1839: a zombie (exited, unreaped child) must NOT count as alive.
func Test1839ZombieNotAlive(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("/proc unavailable; signal-0-only behavior documented")
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	// Exit without Wait -> the child becomes a zombie owned by us until
	// test cleanup; poll briefly for the Z state to appear.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if isZombieUnix(pid) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !isZombieUnix(pid) {
		_ = cmd.Wait()
		t.Skip("could not produce a zombie (fast reaping host)")
	}
	if IsProcessAlive(pid) {
		_ = cmd.Wait()
		t.Fatalf("zombie pid %d must not be reported alive", pid)
	}
	_ = cmd.Wait()
}

// Normal and non-existent processes keep their verdicts.
func Test1839VerdictsUnchanged(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("self must be alive")
	}
	if IsProcessAlive(1 << 22) {
		t.Fatal("unlikely pid must be dead")
	}
}
