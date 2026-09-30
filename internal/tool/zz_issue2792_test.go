package tool

// #2792 regression: the grep/readlink/date/stat arms used unanchored
// substring matching ("cmd " prefix anywhere at the start of the WHOLE
// command + " -x" contained ANYWHERE in it), so a flag on a later
// pipeline/chain segment (curl -P, sort -f, find -print...) misfired the
// arm for a command that never used the GNU flag. The #1703 xargs -r fix
// established the anchored pattern (segment the command, match the flag
// as an exact token of the command's own segment); these four arms were
// the named-but-unfixed remainder.

import (
	"runtime"
	"testing"
)

// All probes go through diagnoseShellCompat (full path incl. ToLower),
// never calling pattern.match directly.
func TestIssue2792_LaterSegmentFlagsDoNotMisfire(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("diagnoseShellCompat is BSD/macOS-only")
	}
	cases := []struct {
		name string
		cmd  string
	}{
		{
			// Issue scenario 1: -P belongs to curl, not grep.
			name: "curl -P after grep does not fire the grep -P arm",
			cmd:  "grep -q error app.log | curl -P 0 ftp://example.com",
		},
		{
			// Issue scenario 2: -print contains the " -p" prefix.
			name: "find -print after grep does not fire the grep -P arm",
			cmd:  "grep -q TODO file && find . -name '*.log' -print",
		},
		{
			// Issue scenario 3: -F is a legal BSD sort flag.
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

// Genuine GNU-flag usage must still fire (segment-anchored exact token).
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
