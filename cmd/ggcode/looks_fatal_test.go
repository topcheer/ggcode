package main

import "testing"

// looksFatal gates when the stderr capture reader starts persisting the
// stream to disk. These banners MUST trigger capture; ordinary log lines
// MUST NOT (a false positive would write a bogus crash file on every run).
func TestLooksFatal(t *testing.T) {
	fatal := []string{
		"fatal error: concurrent map read and map write",
		"fatal error: out of memory: cannot allocate 36-byte blocks (x times)",
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"panic: something",
		"SIGSEGV: segmentation violation",
		"SIGABRT: abort",
		"goroutine 2120 [running]:",
		"goroutine 1 [running]:",
	}
	for _, line := range fatal {
		if !looksFatal(line) {
			t.Errorf("expected fatal for %q", line)
		}
	}
	benign := []string{
		"",
		"2026/09/05 02:00:00 some library noise",
		"goroutine 26634 [select]:",
		"goroutine 26611 [IO wait]:",
		"warning: MCP server x failed: boom",
		"fatal errors are bad",          // prefix must be exact
		"panicked once upon a time",     // prefix must be exact
		"prefix goroutine 1 [running]:", // must be line start
	}
	for _, line := range benign {
		if looksFatal(line) {
			t.Errorf("expected benign for %q", line)
		}
	}
}
