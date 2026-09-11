package util

// #1837 case 1 regression: on Windows, an MSYS/Git-Bash style HOME
// (/c/Users/x) must NOT be trusted - Go's standard library deliberately
// ignores HOME on Windows, and honoring the POSIX-form value split the
// process into two homes (this helper vs os.UserHomeDir callers).

import (
	"runtime"
	"testing"
)

func TestIsWindowsStylePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`C:\Users\alice`, true},
		{`D:/data/home`, true},
		{`\\server\share\home`, true},
		{`/c/Users/alice`, false}, // MSYS form - the bug trigger
		{`/home/alice`, false},
		{`C:`, false},   // no separator after colon
		{`1:\x`, false}, // not a letter
		{`c`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isWindowsStylePath(c.in); got != c.want {
			t.Errorf("isWindowsStylePath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHomeDirHomeOverrideRespectedOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix semantics")
	}
	t.Setenv("HOME", "/tmp/homelike-1837")
	if got := HomeDir(); got != "/tmp/homelike-1837" {
		t.Fatalf("HomeDir() = %q, want the HOME override", got)
	}
}
