//go:build windows

package util

// #1837 case 1: the MSYS HOME must be skipped in favor of USERPROFILE so
// the helper agrees with os.UserHomeDir callers in the same process.

import (
	"testing"
)

func TestHomeDirIgnoresMSYSStyleHomeOnWindows(t *testing.T) {
	t.Setenv("HOME", `/c/Users/alice`)
	t.Setenv("USERPROFILE", `C:\Users\alice`)
	got := HomeDir()
	if got == `/c/Users/alice` {
		t.Fatalf("HomeDir() returned the MSYS-style HOME verbatim: %q (drive-root-relative config split)", got)
	}
	if got != `C:\Users\alice` {
		t.Fatalf("HomeDir() = %q, want USERPROFILE resolution", got)
	}
}

func TestHomeDirHonorsWindowsStyleHomeOverride(t *testing.T) {
	t.Setenv("HOME", `E:\ci\home`)
	t.Setenv("USERPROFILE", `C:\Users\alice`)
	if got := HomeDir(); got != `E:\ci\home` {
		t.Fatalf("HomeDir() = %q, want the Windows-form HOME override", got)
	}
}
