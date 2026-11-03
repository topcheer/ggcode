package tool

import "testing"

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

func TestCommandGateContinuationJoined(t *testing.T) {
	g := NewCommandGate()

	assembled := "rm " + "-rf " + "/etc"
	split := "rm \\\n" + "-rf \\\n" + "/etc"
	if r := g.Check(assembled); !r.IsBlocked() {
		t.Fatalf("control: assembled form should block")
	}
	if r := g.Check(split); !r.IsBlocked() {
		t.Fatalf("#1648-3: backslash continuation bypassed the gate:\n%q", split)
	}

	splitCRLF := "rm \\\r\n" + "-rf \\\r\n" + "/etc"
	if r := g.Check(splitCRLF); !r.IsBlocked() {
		t.Fatalf("#1648-3: CRLF continuation bypassed the gate")
	}
}
