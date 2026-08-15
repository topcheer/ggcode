package tool

import "testing"

// #410: the dispersed rm-flag block rule must only evaluate the rm
// sub-command itself. Benign compound commands whose LATER segments
// contribute the "missing" recursive flag / path anchor must not be blocked.
func TestGateRmLongFlagNoCrossSeparator(t *testing.T) {
	g := NewCommandGate()

	benign := []string{
		// rm contributes only F; the R flag and /etc path anchor come from
		// a later, unrelated grep segment — previously hard-blocked (#410).
		"rm -f old-backup.tar && grep --recursive pattern /etc/hosts",
		"rm -f old.tar; grep -r x /etc",
		"rm -f junk | grep -r foo /etc/passwd",
		"echo hi && rm -f x && grep -r y /etc/hosts",
	}
	for _, cmd := range benign {
		if r := g.Check(cmd); r.IsBlocked() {
			t.Errorf("Check(%q) blocked across command separators: %s", cmd, r.Reason)
		}
	}

	malicious := []string{
		// Dispersed dangerous forms within ONE rm command must still block.
		"rm --force /etc --recursive",
		"rm /etc --force --recursive",
		"rm -f /etc -r",
	}
	for _, cmd := range malicious {
		if r := g.Check(cmd); !r.IsBlocked() {
			t.Errorf("Check(%q) should block dispersed rm on critical path, got %v", cmd, r.Behavior)
		}
	}
}
