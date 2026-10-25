//go:build goolm

package main

// #4397 regression tests: parseA2ATimeout must warn on stderr when an
// explicitly-configured a2a.task_timeout fails to parse ("90sec", "5",
// trailing spaces) instead of silently falling back to 5m - the same
// "silent fallbacks must be diagnosable" principle #4393 established for
// adhocInt. Empty string stays a legal silent default (unset).
// parseA2AMaxTasks stays silent on 0 (#1422-A first-run default fill is
// skipped by --bypass installs; warning would spam fresh installs).

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestIssue4397_ParseA2ATimeoutWarnsOnInvalidValue(t *testing.T) {
	cases := []struct {
		in       string
		wantDur  time.Duration
		wantWarn bool
	}{
		{"", 5 * time.Minute, false},       // unset: silent default
		{"90s", 90 * time.Second, false},   // valid: no warning
		{"1h30m", 90 * time.Minute, false}, // valid compound
		{"90sec", 5 * time.Minute, true},   // typo'd unit
		{"90s ", 5 * time.Minute, true},    // trailing space
		{"5", 5 * time.Minute, true},       // missing unit
	}
	for _, c := range cases {
		orig := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		got := parseA2ATimeout(c.in)
		w.Close()
		os.Stderr = orig
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		if got != c.wantDur {
			t.Fatalf("parseA2ATimeout(%q) = %v, want %v", c.in, got, c.wantDur)
		}
		warned := bytes.Contains(buf.Bytes(), []byte("a2a.task_timeout"))
		if warned != c.wantWarn {
			t.Fatalf("parseA2ATimeout(%q) stderr warned = %v, want %v (stderr: %q)", c.in, warned, c.wantWarn, buf.String())
		}
		if warned {
			if !bytes.Contains(buf.Bytes(), []byte("warning:")) || !bytes.Contains(buf.Bytes(), []byte(c.in)) {
				t.Fatalf("warning must name the field and the rejected value, got %q", buf.String())
			}
		}
	}
}
