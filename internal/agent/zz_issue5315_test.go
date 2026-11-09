package agent

import "testing"

// #5315: the rm_rf lexical gate matched the raw command string, so any
// command whose TEXT mentioned the pattern (heredoc payloads, echo/grep
// arguments, log appends) tripped a critical guard. Fix: strip heredoc
// bodies and require rm at a command position (segment start after
// ;&&|||& or newline, with optional env-prefix assignments).

func hasRmRf(cmd string) bool {
	for _, p := range detectDestructiveInShellCommand(cmd) {
		if p.name == "rm_rf" {
			return true
		}
	}
	return false
}

func TestIssue5315_TextMentionsDoNotFire(t *testing.T) {
	negatives := []string{
		// heredoc body containing the pattern
		"cat >> .ggcode/self-observations.md <<'EOF'\nrm -rf /\nsome logged note\nEOF",
		// echo of the words
		`echo "rm -rf /tmp/x"`,
		// grep pattern arg
		"grep 'rm -rf' build.log",
		// quoted mention inside a compound command
		"echo hi && echo 'never run rm -rf /' && ls",
		// heredoc with <<- form and trailing comment
		"cat > x.md <<-EOT # log\nrm -rf /\nEOT",
	}
	for i, cmd := range negatives {
		if hasRmRf(cmd) {
			t.Errorf("negative %d fired on text-only mention: %q", i, cmd)
		}
	}
}

func TestIssue5315_ActualInvocationsStillFire(t *testing.T) {
	positives := []string{
		"rm -Rf tmp",                              // legacy pin parity
		"rm -r -f tmp",                            // legacy pin parity
		"rm --recursive --force tmp",              // legacy pin parity
		"echo hi && rm -rf tmp",                   // chain segment
		"cd x; rm -rf tmp",                        // ; separator
		"FOO=1 BAR=2 rm -rf tmp",                  // env-prefix assignments
		"cat > a <<'EOF'\nhello\nEOF\nrm -rf tmp", // rm after closed heredoc
	}
	for i, cmd := range positives {
		if !hasRmRf(cmd) {
			t.Errorf("positive %d did NOT fire on real invocation: %q", i, cmd)
		}
	}
}

func TestIssue5315_HeredocStripDropsPayloadAndTerminator(t *testing.T) {
	// payload AND terminator line are dropped by design (the terminator
	// is just the marker word); surrounding lines survive
	in := "cat >> f <<'EOF'\nrm -rf /\nEOF\nls"
	got := stripHeredocBodies(in)
	if got != "cat >> f <<'EOF'\nls" {
		t.Errorf("strip output unexpected: %q", got)
	}
}
