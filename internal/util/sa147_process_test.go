//go:build !windows

package util

// sa-147 process.go coverage: edge branches of the PID-liveness helpers.
// Windows ships its own IsProcessAlive (process_windows.go); itoa and
// isZombieUnix only exist on this side of the build tag.

import "testing"

func TestItoaEdgeCases(t *testing.T) {
	cases := map[int]string{
		0:           "0",
		1:           "1",
		-1:          "-1",
		42:          "42",
		-42:         "-42",
		1234567890:  "1234567890",
		2147483647:  "2147483647",
		-2147483647: "-2147483647",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestIsZombieUnixNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1, -99999} {
		if isZombieUnix(pid) {
			t.Errorf("isZombieUnix(%d) = true, want false", pid)
		}
	}
}

func TestIsZombieUnixMissingProcess(t *testing.T) {
	// Missing /proc entry (or no /proc at all, e.g. darwin): the signal-0
	// verdict stays authoritative - must report not-zombie, never hang.
	if isZombieUnix(1 << 22) {
		t.Fatal("nonexistent pid reported as zombie")
	}
}

// EPERM companion (#2190): PID 1 always answers signal 0 - success (root)
// or EPERM (unprivileged) - and must be reported alive either way.
func TestIsProcessAlivePid1(t *testing.T) {
	if !IsProcessAlive(1) {
		t.Fatal("pid 1 must be reported alive (EPERM and success both mean existing)")
	}
}
