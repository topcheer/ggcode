package tool

import (
	"strings"
	"testing"
)

// TestIssue4923B_AskQuotedInert verifies Bug B: ask-layer rm/DROP/sudo/DELETE
// rules evaluate the inert-unwrapped view, so quoted TEXT and heredoc bodies
// (argument data the shell never executes) no longer fire an Ask, while the
// #3125 quoted-flag defense (`rm "-rf" x`) still fires.
func TestIssue4923B_AskQuotedInert(t *testing.T) {
	g := NewCommandGate()

	mustAsk := func(cmd string) {
		t.Helper()
		if r := g.Check(cmd); r.Behavior != Ask && r.Behavior != Block {
			t.Errorf("%q: expected Ask (or Block), got %v (%s)", cmd, r.Behavior, r.Reason)
		}
	}
	mustNotAskRM := func(cmd, family string) {
		t.Helper()
		r := g.Check(cmd)
		if r.Behavior == Ask && strings.Contains(r.Reason, family) {
			t.Errorf("%q: %s false-positive fired: %s", cmd, family, r.Reason)
		}
	}

	// False positives that MUST NOT fire (#4923 Bug B).
	mustNotAskRM(`echo "rm -rf demo"`, "delete")
	mustNotAskRM(`echo 'sudo make me a sandwich'`, "sudo")
	mustNotAskRM("cat > migration.sql <<'EOF'\nDROP TABLE users;\nDELETE FROM orders;\nEOF", "database")

	// Real dangerous commands still Ask (unquoted, real invocations).
	mustAsk("rm -rf /tmp/build")
	mustAsk("sudo apt install thing")
	mustAsk("echo x; DROP TABLE users")

	// #3125 defense preserved: quote-wrapped SIMPLE tokens unwrap first.
	mustAsk(`rm "-rf" /tmp/x`)
	mustAsk(`rm '-f' /tmp/x`)
}
