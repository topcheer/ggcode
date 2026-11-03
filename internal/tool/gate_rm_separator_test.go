package tool

import "testing"

func TestGateRmLongFlagNoCrossSeparator(t *testing.T) {
	g := NewCommandGate()

	benign := []string{

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
