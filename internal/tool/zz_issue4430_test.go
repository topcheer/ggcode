package tool

import (
	"strings"
	"testing"
)

// #4430 companion tests (bugs 1+2; 3 analyzed in the issue thread, 4 below).

// Bug 1: `<<<` is a here-STRING - the following lines are live commands, so
// a catastrophic rm there must stay Block (was downgraded to Ask because the
// heredoc opener regex matched from the 2nd '<' and blanked the body).
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

// Bug 2: the tilde word-boundary in rmP was a dead anchor - dispersed long
// flags wiping the home directory hit NO rule and ran Allowed.
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

// Benign rm shapes must stay unaffected by the tilde/here-string tightening.
func TestIssue4430_BenignRmUnaffected(t *testing.T) {
	gate := NewCommandGate()
	for _, cmd := range []string{
		"rm -f note.txt",               // plain file - Allow/Ask, never Block
		"rm -rf /tmp/build-cache",      // benign prefix
		"rm -rf ./build",               // relative
		"cat << EOF\nrm -rf /etc\nEOF", // REAL heredoc body stays inert (quotedInert) - Allow/Ask
	} {
		if r := gate.Check(cmd); r.Behavior == Block {
			t.Fatalf("benign command must not Block: %q (%s)", cmd, r.Reason)
		}
	}
}

// Bug 4: the GIT_PAGER clean rule's `\S+` stopped at the first space of a
// quoted override (`GIT_PAGER="less -R"` left ` -R"` behind), producing an
// unbalanced-quote, invalid-shell CleanedCmd. The rule now consumes the
// quoted form fully.
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
