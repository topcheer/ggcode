package util

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
		{`/c/Users/alice`, false},
		{`/home/alice`, false},
		{`C:`, false},
		{`1:\x`, false},
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
