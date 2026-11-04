package main

import "testing"

// Windows terminals (cmd/PowerShell/Windows Terminal) do not set TERM by
// default; an empty TERM there must NOT select the plain frontend
// (regression reported 2026-11-04, introduced by #4964's detection).
func TestTermLooksDumbWindowsEmptyTerm(t *testing.T) {
	if termLooksDumb("", true) {
		t.Fatal("windows: empty TERM must not be treated as dumb")
	}
	if termLooksDumb("xterm-256color", true) {
		t.Fatal("windows: real TERM value must not be treated as dumb")
	}
	if !termLooksDumb("dumb", true) {
		t.Fatal("windows: explicit TERM=dumb must select plain")
	}
	if !termLooksDumb("DUMB", true) {
		t.Fatal("windows: TERM=dumb is case-insensitive")
	}
}

// Unix behavior unchanged: empty TERM still means dumb/non-standard env.
func TestTermLooksDumbUnix(t *testing.T) {
	if !termLooksDumb("", false) {
		t.Fatal("unix: empty TERM must be treated as dumb")
	}
	if !termLooksDumb("dumb", false) {
		t.Fatal("unix: TERM=dumb must select plain")
	}
	if termLooksDumb("xterm", false) {
		t.Fatal("unix: real TERM must not be treated as dumb")
	}
}
