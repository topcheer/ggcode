package tool

import (
	"runtime"
	"testing"
)

func TestIssue2792_LaterSegmentFlagsDoNotMisfire(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("diagnoseShellCompat is BSD/macOS-only")
	}
	cases := []struct {
		name string
		cmd  string
	}{
		{

			name: "curl -P after grep does not fire the grep -P arm",
			cmd:  "grep -q error app.log | curl -P 0 ftp://example.com",
		},
		{

			name: "find -print after grep does not fire the grep -P arm",
			cmd:  "grep -q TODO file && find . -name '*.log' -print",
		},
		{

			name: "sort -f after readlink does not fire the readlink -f arm",
			cmd:  "readlink link | sort -f",
		},
		{
			name: "curl -D after date does not fire the date -d arm",
			cmd:  "date +%s | curl -D headers.txt https://example.com",
		},
		{
			name: "tail -c after stat does not fire the stat -c arm",
			cmd:  "stat file | tail -c 20",
		},
	}
	for _, tc := range cases {
		if got := diagnoseShellCompat(tc.cmd, "", "some failure output"); got != "" {
			t.Errorf("#2792 %s: misfired with %q", tc.name, got)
		}
	}
}

func TestIssue2792_GenuineFlagsStillFire(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("diagnoseShellCompat is BSD/macOS-only")
	}
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "grep -P as first segment fires",
			cmd:  `grep -P '\d+' file`,
			want: "grep -P (PCRE) is GNU-only",
		},
		{
			name: "grep -P mid-pipeline fires (segment anchoring also fixes the old whole-command-prefix blind spot)",
			cmd:  "cat big.log | grep -P 'err\\d+' | head -5",
			want: "grep -P (PCRE) is GNU-only",
		},
		{
			name: "readlink -f fires",
			cmd:  "readlink -f /usr/local/bin/python3",
			want: "readlink -f is GNU-only",
		},
		{
			name: "readlink --canonicalize fires",
			cmd:  "readlink --canonicalize link",
			want: "readlink -f is GNU-only",
		},
		{
			name: "date -d fires",
			cmd:  "date -d 'yesterday' +%Y-%m-%d",
			want: "date -d is GNU-only",
		},
		{
			name: "stat -c fires",
			cmd:  "stat -c '%s' file.txt",
			want: "stat -c is GNU-only",
		},
		{
			name: "stat --format fires",
			cmd:  "stat --format '%s' file.txt",
			want: "stat -c is GNU-only",
		},
	}
	for _, tc := range cases {
		got := diagnoseShellCompat(tc.cmd, "", "")
		if got == "" {
			t.Errorf("#2792 %s: genuine GNU flag no longer fires", tc.name)
		}
	}
}
