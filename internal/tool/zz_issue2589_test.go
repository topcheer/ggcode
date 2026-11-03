//go:build darwin

package tool

import (
	"strings"
	"testing"
)

func TestIssue2589_SplitCdOnlyWhenCommandEmpty(t *testing.T) {
	part := buildSplitCmdPart("", "/Volumes/new/ggai/k8ops")
	if !strings.Contains(part, `input text "cd '/Volumes/new/ggai/k8ops'" to newTerm`) {
		t.Fatalf("command empty must emit cd-only input text, got: %q", part)
	}
	if strings.Contains(part, "&&") {
		t.Fatalf("cd-only branch must not carry a command, got: %q", part)
	}
}

func TestIssue2589_SplitCommandBranchKeeps832Behavior(t *testing.T) {
	part := buildSplitCmdPart("go test ./...", "/Volumes/new ggai")
	if !strings.Contains(part, `input text "cd '/Volumes/new ggai' && go test ./..." to newTerm`) {
		t.Fatalf("command branch must keep #832 combined cd+cmd, got: %q", part)
	}
}

func TestIssue2589_SplitCdOnlyEscapesCraftedWd(t *testing.T) {
	part := buildSplitCmdPart("", `'; rm -rf /tmp/x; #'`)
	if strings.Contains(part, `cd ''; rm -rf`) {
		t.Fatalf("crafted wd passed through unescaped, got: %q", part)
	}
	if !strings.Contains(part, "rm -rf") {
		t.Fatalf("payload should survive as inert text (escaped, not erased), got: %q", part)
	}
}
