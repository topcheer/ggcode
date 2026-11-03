//go:build !windows

package util

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

	if isZombieUnix(1 << 22) {
		t.Fatal("nonexistent pid reported as zombie")
	}
}

func TestIsProcessAlivePid1(t *testing.T) {
	if !IsProcessAlive(1) {
		t.Fatal("pid 1 must be reported alive (EPERM and success both mean existing)")
	}
}
