package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// #4393: adhocInt must WARN on stderr when an explicitly-set env value is
// invalid (unparseable or non-positive) instead of silently falling back to
// the default. Flag override and unset/blank env stay silent; valid values
// return without warning.

// captureStderrInt runs fn with os.Stderr swapped to a pipe and returns
// fn's result plus everything fn wrote to stderr.
func captureStderrInt(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	v := fn()
	w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return v, string(b)
}

func TestIssue4393AdhocIntWarnsOnInvalidEnv(t *testing.T) {
	cases := []struct {
		name        string
		flagVal     int
		env         string
		wantValue   int
		wantWarning bool
		wantText    string
	}{
		{
			name:        "unparseable value warns",
			env:         "64k",
			wantValue:   0,
			wantWarning: true,
			wantText:    `GGCODE_LLM_CONTEXT_WINDOW="64k"`,
		},
		{
			name:        "zero warns",
			env:         "0",
			wantValue:   0,
			wantWarning: true,
			wantText:    "must be a positive integer",
		},
		{
			name:        "negative warns",
			env:         "-5",
			wantValue:   0,
			wantWarning: true,
			wantText:    "must be a positive integer",
		},
		{
			name:      "valid value returns silently",
			env:       "65536",
			wantValue: 65536,
		},
		{
			name:      "unset env stays silent",
			env:       "",
			wantValue: 0,
		},
		{
			name:      "whitespace-only env stays silent",
			env:       "   ",
			wantValue: 0,
		},
		{
			name:      "flag wins over invalid env, no env read, no warning",
			flagVal:   4096,
			env:       "64k",
			wantValue: 4096,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				os.Unsetenv(adhocEnvContextWindow)
			} else {
				t.Setenv(adhocEnvContextWindow, tc.env)
			}
			got, stderr := captureStderrInt(t, func() int {
				return adhocInt(tc.flagVal, adhocEnvContextWindow)
			})
			if got != tc.wantValue {
				t.Fatalf("adhocInt(%d, %q) = %d, want %d", tc.flagVal, tc.env, got, tc.wantValue)
			}
			if tc.wantWarning {
				if !strings.Contains(stderr, "WARNING: ignoring invalid") {
					t.Fatalf("missing WARNING on stderr, got: %q", stderr)
				}
				if !strings.Contains(stderr, tc.wantText) {
					t.Fatalf("warning missing %q, got: %q", tc.wantText, stderr)
				}
			} else if strings.Contains(stderr, "WARNING") {
				t.Fatalf("unexpected warning on stderr: %q", stderr)
			}
		})
	}
}
