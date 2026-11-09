package agent

import "testing"

// #5342 increment (carrier fix/issue-5342, rebased onto main after #5347
// merged). Scope per arbitration: keep #5347's wrapper-arg stripping as-is
// and add only the pieces main still lacks -
//
//   - chain continuation: firstRmCommandSegment used to return the FIRST
//     rm segment even when it was non-recursive, so `rm x && rm -rf d`
//     never surfaced its recursive force-delete (the carrier fix's core).
//   - #5345's three classes: doas (permission-layer rmCmdAnchor parity),
//     path-form wrappers (`/usr/bin/sudo rm -rf`), and wrapper + path-form
//     rm (`sudo /bin/rm -rf`), all normalized to the final path component
//     before table/word comparison - same normalization philosophy as
//     heredocReceiverKeepsBody.
//   - reviewer conditional pins on #5345: `command -v rm` (query verb)
//     stays silent while `command rm -rf x` (wrapper verb) fires, and
//     multi-rm chains produce exactly ONE rm_rf advisory - pinned
//     as-implemented to prevent silent drift.
//
// Credit: doas/path shapes adapted from #5345; the four text-mention
// negatives and the wrapper-arg positives originated in #5347 (already on
// main; the negatives are retained here as contract pins for this
// increment).

func rmRfCount5342(cmd string) int {
	n := 0
	for _, p := range detectDestructiveInShellCommand(cmd) {
		if p.name == "rm_rf" {
			n++
		}
	}
	return n
}

// FN controls: chains whose first rm segment is plain rm must still
// surface the later recursive force-delete.
func TestIssue5342IncrementChainContinuationFires(t *testing.T) {
	positives := []string{
		"rm file.txt && rm -rf /tmp/x",
		"rm file.txt; rm -rf /tmp/x",
		"rm file.txt | rm -rf /tmp/x",
		"rm file.txt\nrm -rf /tmp/x",
		"cd /tmp && rm old && rm -rf build",
		"rm plain && sudo rm -rf /tmp/x",
		"echo hi && rm -rf /tmp/x",
		"rm x && doas rm -rf /etc/thing",
	}
	for i, cmd := range positives {
		if rmRfCount5342(cmd) == 0 {
			t.Errorf("positive %d: later-segment rm -rf must fire (chain short-circuit regression): %q", i, cmd)
		}
	}
}

// FN controls: #5345's three classes (doas / path wrapper / wrapper+path rm).
func TestIssue5342IncrementWrapperPathFormsFire(t *testing.T) {
	positives := []string{
		"doas rm -rf /tmp/x",
		"/usr/bin/sudo rm -rf /tmp/x",
		"sudo /bin/rm -rf /tmp/x",
		"/usr/bin/doas rm -rf /tmp/x",
		"/usr/bin/sudo -u root /bin/rm -rf /tmp/x",
		"doas /bin/rm -rf /tmp/x",
	}
	for i, cmd := range positives {
		if rmRfCount5342(cmd) == 0 {
			t.Errorf("positive %d: doas/path wrapper form must fire (#5345 class): %q", i, cmd)
		}
	}
}

// Reviewer pin 1 (on #5345): `command -v rm` queries the shell builtin,
// it does not invoke rm; `command rm -rf x` runs it. Query silent,
// invocation fires.
func TestIssue5342IncrementCommandVBoundaryPin(t *testing.T) {
	if got := rmRfCount5342("command -v rm"); got != 0 {
		t.Errorf("command -v rm must stay silent (query, not invocation), got %d rm_rf", got)
	}
	if got := rmRfCount5342("command rm -rf /tmp/x"); got != 1 {
		t.Errorf("command rm -rf must fire exactly once, got %d", got)
	}
}

// Reviewer pin 2 (on #5345): advisory semantics for multi-rm chains.
// As implemented: the first matching recursive segment wins and the
// detector emits a single rm_rf pattern per command, so a chain with two
// rm -rf segments still advises once. Pinned to surface any future drift.
func TestIssue5342IncrementMultiRmAdvisorySemanticsPin(t *testing.T) {
	for _, cmd := range []string{
		"rm -rf a && rm -rf b",
		"rm -rf a; rm -rf b; rm -rf c",
		"rm a && rm -rf b && sudo rm -rf c",
	} {
		if got := rmRfCount5342(cmd); got != 1 {
			t.Errorf("multi-rm chain %q: want exactly 1 rm_rf advisory, got %d", cmd, got)
		}
	}
}

// FP contract: #5347's four text-mention negatives plus the wrapper
// stop-word pins must hold under the increment.
func TestIssue5342IncrementTextMentionNegativesHold(t *testing.T) {
	negatives := []string{
		"echo 'sudo rm -rf /tmp/x'",
		"grep 'nohup rm -rf' build.log",
		"cat >> ledger.md <<'EOF'\nsudo rm -rf /\nlog note\nEOF",
		"git commit -m \"fix: drop xargs rm -rf usage\"",
		"git rm -rf --cached .", // git rm is not /bin/rm
		"sudo ls -la /tmp",      // wrapper without rm behind it
	}
	for i, cmd := range negatives {
		if got := rmRfCount5342(cmd); got != 0 {
			t.Errorf("negative %d fired on non-invocation text: %q", i, cmd)
		}
	}
}
