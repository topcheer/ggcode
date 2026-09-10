package util

import "testing"

// #1842: the System32 WSL entry point must be rejected so commands don't
// silently execute inside the Linux subsystem.
func Test1842WSLBashPathRejected(t *testing.T) {
	for _, p := range []string{
		`C:\Windows\System32\bash.exe`,
		`C:\WINDOWS\system32\Bash.EXE`, // case-insensitive
		`D:\Win10\Windows\System32\bash.exe`,
	} {
		if !isWSLBashPath(p) {
			t.Errorf("WSL entry not rejected: %s", p)
		}
	}
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Users\me\scoop\apps\git\current\bin\bash.exe`,
		`/usr/bin/bash`, // non-Windows passthrough
	} {
		if isWSLBashPath(p) {
			t.Errorf("real bash wrongly rejected: %s", p)
		}
	}
}
