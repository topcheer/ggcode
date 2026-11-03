//go:build !windows

package util

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func Test1839ZombieNotAlive(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("/proc unavailable; signal-0-only behavior documented")
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid

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

func Test1839VerdictsUnchanged(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("self must be alive")
	}
	if IsProcessAlive(1 << 22) {
		t.Fatal("unlikely pid must be dead")
	}
}
