//go:build goolm

package main

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestIssue4397_ParseA2ATimeoutWarnsOnInvalidValue(t *testing.T) {
	cases := []struct {
		in       string
		wantDur  time.Duration
		wantWarn bool
	}{
		{"", 5 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"1h30m", 90 * time.Minute, false},
		{"90sec", 5 * time.Minute, true},
		{"90s ", 5 * time.Minute, true},
		{"5", 5 * time.Minute, true},
	}
	for _, c := range cases {

		var buf bytes.Buffer
		orig := a2aTimeoutWarnf
		a2aTimeoutWarnf = func(format string, args ...any) {
			fmt.Fprintf(&buf, format, args...)
		}
		got := parseA2ATimeout(c.in)
		a2aTimeoutWarnf = orig
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
