package tool

import (
	"testing"
)

// #4430 companion tests (bugs 1+2; 3+4 analyzed in the issue thread).

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
			t.Fatalf("here-string body must stay catastrophic: %q -> %v (%s)", cmd, r.Behavior, r.Reason)
		}
	}
}

// Bug 2: bare `~` and `~/` deletion of the whole home directory must Block.
func TestIssue4430_TildeHomeDeletionBlocks(t *testing.T) {
	gate := NewCommandGate()
	for _, cmd := range []string{
		"rm --recursive ~ --force", // permutation rule (rmP) - was Allowed
		"rm -f ~ -r",               // permutation rule - was Ask
		"rm -rf ~",                 // primary rule - bare ~ was missing
		"rm -rf ~/",                // already covered, must stay Blocked
	} {
		if r := gate.Check(cmd); r.Behavior != Block {
			t.Fatalf("home-directory wipe must Block: %q -> %v (%s)", cmd, r.Behavior, r.Reason)
		}
	}
}

// Regression guard: the fix must not widen onto routine rm usage.
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
