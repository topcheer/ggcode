package tool

import (
	"strings"
	"testing"
)

func TestIssue4430_HereStringLinesStayLive(t *testing.T) {
	gate := NewCommandGate()
	for _, cmd := range []string{
		"cat <<< EOF\nrm -rf /etc\nEOF",
		"cat <<<EOF\nrm -rf /etc\nEOF",
		"cat <<<'EOF'\nrm --force --recursive /etc\nEOF",
	} {
		if r := gate.Check(cmd); r.Behavior != Block {
			t.Fatalf("here-string line must stay live (Block): %q -> %v (%s)", cmd, r.Behavior, r.Reason)
		}
	}
}

func TestIssue4430_TildeHomeDeletionBlocks(t *testing.T) {
	gate := NewCommandGate()
	for _, cmd := range []string{
		"rm --recursive ~ --force",
		"rm -f ~ -r",
		"rm -rf ~",
		"rm -rf ~/",
	} {
		if r := gate.Check(cmd); r.Behavior == Allow {
			t.Fatalf("home deletion must never be Allowed: %q (%s)", cmd, r.Reason)
		}
	}
}

func TestIssue4430_BenignRmUnaffected(t *testing.T) {
	gate := NewCommandGate()
	for _, cmd := range []string{
		"rm -f note.txt",
		"rm -rf /tmp/build-cache",
		"rm -rf ./build",
		"cat << EOF\nrm -rf /etc\nEOF",
	} {
		if r := gate.Check(cmd); r.Behavior == Block {
			t.Fatalf("benign command must not Block: %q (%s)", cmd, r.Reason)
		}
	}
}

func TestIssue4430_Bug4_GITPAGERQuoteAwareClean(t *testing.T) {
	g := NewCommandGate()
	for name, cmd := range map[string]string{
		"double-quoted with space": "GIT_PAGER=\"less -R\" git log",
		"single-quoted with space": "GIT_PAGER='less -R' git log",
		"double-quoted no space":   "GIT_PAGER=\"cat\" git log",
		"unquoted":                 "GIT_PAGER=cat git log",
	} {
		res := g.Check(cmd)
		if res.CleanedCmd == "" {
			t.Fatalf("%s: override not stripped at all: %q", name, res.CleanedCmd)
		}
		if n := strings.Count(res.CleanedCmd, "'") + strings.Count(res.CleanedCmd, "\""); n%2 != 0 {
			t.Fatalf("%s: CleanedCmd has unbalanced quotes: %q", name, res.CleanedCmd)
		}
	}
}
