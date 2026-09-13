package tool

import "testing"

// #1648 case 2 probes: grepping text that CONTAINS disaster keywords must
// not hard-Block (quotedInert parity with the rm family).
func TestCommandGateQuotedDisasterTextAllowed(t *testing.T) {
	g := NewCommandGate()
	for _, cmd := range []string{
		`grep 'shutdown the cluster' CHANGELOG.md`,
		`grep "reboot required" deploy-notes.txt`,
		`cat doc.md | grep 'halt the migration'`,
		`grep 'killall Little Snitch' README.md`,
		`echo "history -c is dangerous" > notes.txt`,
	} {
		if r := g.Check(cmd); r.IsBlocked() {
			t.Fatalf("#1648-2: quoted disaster text blocked: %q -> %s", cmd, r.Reason)
		}
	}
	// Real invocations still block.
	for _, cmd := range []string{
		`shutdown -h now`,
		`reboot`,
		`history -c`,
	} {
		if r := g.Check(cmd); !r.IsBlocked() {
			t.Fatalf("real disaster command allowed: %q", cmd)
		}
	}
}

// #1648 case 3 probe: backslash-newline continuation must not bypass the
// gate - the assembled form is what the shell runs.
func TestCommandGateContinuationJoined(t *testing.T) {
	g := NewCommandGate()
	// rm -rf /etc split across two lines via backslash continuation.
	assembled := "rm " + "-rf " + "/etc" // separate literals: keep THIS test file itself gate-clean
	split := "rm \\\n" + "-rf \\\n" + "/etc"
	if r := g.Check(assembled); !r.IsBlocked() {
		t.Fatalf("control: assembled form should block")
	}
	if r := g.Check(split); !r.IsBlocked() {
		t.Fatalf("#1648-3: backslash continuation bypassed the gate:\n%q", split)
	}
	// CRLF variant.
	splitCRLF := "rm \\\r\n" + "-rf \\\r\n" + "/etc"
	if r := g.Check(splitCRLF); !r.IsBlocked() {
		t.Fatalf("#1648-3: CRLF continuation bypassed the gate")
	}
}
