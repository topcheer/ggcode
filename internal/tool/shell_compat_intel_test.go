package tool

import (
	"runtime"
	"strings"
	"testing"
)

func TestDiagnoseShellCompat(t *testing.T) {
	// On Linux, all GNU commands are native -- function should return empty.
	isLinux := runtime.GOOS == "linux"

	tests := []struct {
		name    string
		command string
		stdout  string
		stderr  string
		want    string // expected substring in output, empty = no match
	}{
		{
			name:    "sed -i GNU syntax fails on BSD",
			command: "sed -i 's/foo/bar/' file.txt",
			stdout:  "",
			stderr:  "sed: 1: \"s/foo/bar/\": extra characters at the end of command",
			want:    "BSD sed requires a backup suffix",
		},
		{
			name:    "readlink -f GNU only flag",
			command: "readlink -f /usr/local/bin/python3",
			stdout:  "",
			stderr:  "readlink: illegal option -- f",
			want:    "readlink -f is GNU-only",
		},
		{
			name:    "grep -P PCRE GNU only",
			command: `grep -P '\d{3}-\d{4}' file.txt`,
			stdout:  "",
			stderr:  "grep: option requires an argument -- P",
			want:    "grep -P (PCRE) is GNU-only",
		},
		{
			name:    "date -d GNU only",
			command: "date -d 'yesterday' +%Y-%m-%d",
			stdout:  "",
			stderr:  "date: illegal option -- d",
			want:    "date -d is GNU-only",
		},
		{
			name:    "stat -c GNU only",
			command: "stat -c '%s' file.txt",
			stdout:  "",
			stderr:  "stat: illegal option -- c",
			want:    "stat -c is GNU-only",
		},
		{
			name:    "find -printf GNU only",
			command: "find . -name '*.go' -printf '%p\\n'",
			stdout:  "",
			stderr:  "find: -printf: unknown primary or operator",
			want:    "find -printf is GNU-only",
		},
		{
			name:    "head -n negative offset GNU only",
			command: "head -n -1 file.txt",
			stdout:  "",
			stderr:  "head: illegal line count -- -1",
			want:    "head -n -N (drop last N lines) is GNU-only",
		},
		{
			name:    "timeout not found on macOS",
			command: "timeout 5 make test",
			stdout:  "",
			stderr:  "timeout: command not found",
			want:    "timeout is GNU coreutils",
		},
		{
			name:    "sort -V version sort GNU only",
			command: "git tag | sort -V",
			stdout:  "",
			stderr:  "sort: unrecognized option `V'",
			want:    "sort -V (version sort) is GNU-only",
		},
		{
			name:    "du --max-depth GNU only",
			command: "du --max-depth=1 -h .",
			stdout:  "",
			stderr:  "",
			want:    "du --max-depth is GNU-only",
		},
		{
			name:    "ls --color GNU only",
			command: "ls --color=auto -la",
			stdout:  "",
			stderr:  "",
			want:    "ls --color and --group-directories are GNU-only",
		},
		{
			name:    "xargs -r GNU only",
			command: "find . -name '*.tmp' | xargs -r rm",
			stdout:  "",
			stderr:  "",
			want:    "xargs -r/--no-run-if-empty is GNU-only",
		},
		{
			name:    "no incompatibility - normal command",
			command: "go build -tags goolm ./...",
			stdout:  "",
			stderr:  "",
			want:    "",
		},
		{
			name:    "no incompatibility - successful ls",
			command: "ls -la",
			stdout:  "file1.txt\nfile2.go",
			stderr:  "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diagnoseShellCompat(tt.command, tt.stdout, tt.stderr)

			if isLinux {
				// On Linux, no compat hints should fire
				if got != "" {
					t.Errorf("on Linux, expected empty result, got: %s", got)
				}
				return
			}

			if tt.want == "" {
				if got != "" {
					t.Errorf("expected no hint, got: %s", got)
				}
				return
			}

			if !strings.Contains(got, tt.want) {
				t.Errorf("diagnoseShellCompat() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestDiagnoseShellCompatFormat(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skipped on Linux")
	}

	got := diagnoseShellCompat("readlink -f /usr/bin/python3", "", "readlink: illegal option -- f")

	if !strings.HasPrefix(got, "[Shell Compat] ") {
		t.Errorf("expected [Shell Compat] prefix, got: %s", got)
	}

	if !strings.Contains(got, "realpath") {
		t.Errorf("expected alternative tool suggestion, got: %s", got)
	}
}
