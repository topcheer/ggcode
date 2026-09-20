//go:build darwin

package tool

import (
	"strings"
	"testing"
)

// TestIssue2589_SplitCdOnlyWhenCommandEmpty pins #2589: ghostty split with
// working_dir set but NO command must still cd the new pane -- the schema
// documents working_dir as an independent parameter ("Working directory for
// the new split/tab/window surface"), and iTerm2's split has the same
// cd-only branch. Before the fix the wd was silently dropped: the pane
// inherited the old cwd, the call still returned success, and later input
// commands ran in the wrong directory.
func TestIssue2589_SplitCdOnlyWhenCommandEmpty(t *testing.T) {
	part := buildSplitCmdPart("", "/Volumes/new/ggai/k8ops")
	if !strings.Contains(part, `input text "cd '/Volumes/new/ggai/k8ops'" to newTerm`) {
		t.Fatalf("command empty must emit cd-only input text, got: %q", part)
	}
	if strings.Contains(part, "&&") {
		t.Fatalf("cd-only branch must not carry a command, got: %q", part)
	}
}

// Command set keeps the #832 combined "cd '...' && cmd" behavior, with the
// same double escaping (shell single-quote + AppleScript).
func TestIssue2589_SplitCommandBranchKeeps832Behavior(t *testing.T) {
	part := buildSplitCmdPart("go test ./...", "/Volumes/new ggai")
	if !strings.Contains(part, `input text "cd '/Volumes/new ggai' && go test ./..." to newTerm`) {
		t.Fatalf("command branch must keep #832 combined cd+cmd, got: %q", part)
	}
}

// A crafted working_dir stays single-quoted on the cd-only branch too:
// escapeShellSingleQuote rewrites each ' so the payload cannot break out of
// the shell single-quoted cd argument, and escapeAS doubles backslashes for
// the AppleScript string layer. Assert the UNESCAPED passthrough form is
// absent (' directly followed by ; rm) -- its presence would mean the
// payload executed as-is. The payload itself survives as inert text.
func TestIssue2589_SplitCdOnlyEscapesCraftedWd(t *testing.T) {
	part := buildSplitCmdPart("", `'; rm -rf /tmp/x; #'`)
	if strings.Contains(part, `cd ''; rm -rf`) {
		t.Fatalf("crafted wd passed through unescaped, got: %q", part)
	}
	if !strings.Contains(part, "rm -rf") {
		t.Fatalf("payload should survive as inert text (escaped, not erased), got: %q", part)
	}
}
