package tool

import (
	"strings"
	"testing"
)

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

	mustNotAskRM(`echo "rm -rf demo"`, "delete")
	mustNotAskRM(`echo 'sudo make me a sandwich'`, "sudo")
	mustNotAskRM("cat > migration.sql <<'EOF'\nDROP TABLE users;\nDELETE FROM orders;\nEOF", "database")

	mustAsk("rm -rf /tmp/build")
	mustAsk("sudo apt install thing")
	mustAsk("echo x; DROP TABLE users")

	mustAsk(`rm "-rf" /tmp/x`)
	mustAsk(`rm '-f' /tmp/x`)
}
