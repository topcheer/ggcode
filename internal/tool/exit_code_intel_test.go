package tool

import (
	"runtime"
	"strings"
	"testing"
)

func TestInterpretExitCode_CommonCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		wantSub  string // substring that must appear
		wantHint bool   // whether a hint should be present
	}{
		{"success", 0, "", false},
		{"generic error", 1, "", false},
		{"not executable", 126, "not executable", true},
		{"not found", 127, "not found", true},
		{"sigint", 130, "SIGINT", true},
		{"sigabrt", 134, "SIGABRT", true},
		{"oom kill", 137, "OOM", true},
		{"segfault", 139, "SIGSEGV", true},
		{"sigterm", 143, "SIGTERM", true},
		{"sigfpe", 136, "SIGFPE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpretExitCode(tt.code)
			if tt.wantSub == "" {
				if got != "" {
					t.Errorf("interpretExitCode(%d) = %q, want empty", tt.code, got)
				}
				return
			}
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("interpretExitCode(%d) = %q, want substring %q", tt.code, got, tt.wantSub)
			}
			if tt.wantHint && !strings.Contains(got, "->") {
				t.Errorf("interpretExitCode(%d) = %q, want a hint (-> ...)", tt.code, got)
			}
		})
	}
}

func TestInterpretExitCode_GenericSignalCodes(t *testing.T) {
	// Unknown signal codes 128+N should still produce a signal label
	got := interpretExitCode(129) // SIGHUP
	if !strings.Contains(got, "signal 1") {
		t.Errorf("interpretExitCode(129) = %q, want 'signal 1'", got)
	}

	// Code outside the known range and not a signal
	got = interpretExitCode(42)
	if got != "" {
		t.Errorf("interpretExitCode(42) = %q, want empty", got)
	}
}

func TestInterpretExitCode_UnknownHighCode(t *testing.T) {
	// 160 is above the signal range and not in the map
	got := interpretExitCode(160)
	if got != "" {
		t.Errorf("interpretExitCode(160) = %q, want empty (outside signal range)", got)
	}
}

// TestExitCodeIntelWindowsBand pins #1683 case 1: the 128+N signal band is
// POSIX-only. On this (non-Windows) test host the band stays live and the
// 126/127 command-not-found entries are platform-independent.
func TestExitCodeIntelWindowsBand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host is windows - band compiled out by design")
	}
	if got := interpretExitCode(137); !strings.Contains(got, "SIGKILL") {
		t.Fatalf("non-windows host must keep signal intel, got %q", got)
	}
	// 126/127 carry meaning on every platform.
	if got := interpretExitCode(127); !strings.Contains(got, "not found") {
		t.Fatalf("127 must stay universal, got %q", got)
	}
}
