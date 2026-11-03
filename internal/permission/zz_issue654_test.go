package permission

import (
	"testing"

	"strings"
)

func TestIssue654RsyncExcludeIncludeBarePatterns(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		want    NetworkRisk
		wantSub string
	}{
		{
			name:    "exclude pattern is not a source operand",
			cmd:     "rsync -a --exclude 'srv:cache' src/ dest/",
			want:    NetworkAccess,
			wantSub: "local copy",
		},
		{
			name:    "include pattern is not a source operand",
			cmd:     "rsync -a --include 'host:path' src/ dest/",
			want:    NetworkAccess,
			wantSub: "local copy",
		},
		{
			name:    "filter pattern is not a destination operand",
			cmd:     "rsync -a ./d host:/x --filter 'a:b'",
			want:    NetworkExfiltrate,
			wantSub: "copying local files to a remote host",
		},
		{
			name:    "equals form still local",
			cmd:     "rsync -a --exclude=srv:cache src/ dest/",
			want:    NetworkAccess,
			wantSub: "local copy",
		},
		{
			name:    "upload direction still detected with exclude",
			cmd:     "rsync -a --exclude 'x:y' ./d host:/x",
			want:    NetworkExfiltrate,
			wantSub: "copying local files to a remote host",
		},
		{
			name:    "download direction still detected with exclude",
			cmd:     "rsync -a --exclude 'x:y' host:/src ./dest",
			want:    NetworkAccess,
			wantSub: "downloading from a remote host",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := tokenizeCommand(tc.cmd)

			start := -1
			for i, tok := range tokens {
				if tok == "rsync" {
					start = i + 1
					break
				}
			}
			if start < 0 {
				t.Fatalf("no rsync token in %q", tc.cmd)
			}
			got := analyzeTransfer(false, tokens, start)
			if got.Risk != tc.want {
				t.Fatalf("risk = %v (%s), want %v", got.Risk, got.Reason, tc.want)
			}
			if !strings.Contains(got.Reason, tc.wantSub) {
				t.Fatalf("reason %q does not contain %q", got.Reason, tc.wantSub)
			}
		})
	}
}

func TestIssue654OptionTakesValue(t *testing.T) {
	for _, name := range []string{"exclude", "include", "filter", "exclude-from", "include-from", "rsh", "files-from"} {
		if !optionTakesValue(false, name) {
			t.Errorf("optionTakesValue(rsync, %q) = false, want true (#654)", name)
		}
	}
	for _, name := range []string{"recursive", "archive", "verbose", "delete", "compress"} {
		if optionTakesValue(false, name) {
			t.Errorf("optionTakesValue(rsync, %q) = true, want false", name)
		}
	}

	for _, name := range []string{"i", "P", "o"} {
		if !optionTakesValue(true, name) {
			t.Errorf("optionTakesValue(scp, %q) = false, want true", name)
		}
	}
	if optionTakesValue(true, "exclude") {
		t.Error("scp has no --exclude taking a value")
	}
}
