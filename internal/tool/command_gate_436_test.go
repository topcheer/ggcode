package tool

import "testing"

func TestCommandGateMultilineNotBlocked(t *testing.T) {
	g := NewCommandGate()
	cmd := "# comment\nrm -f backup.tar\ngrep -r pattern /etc/hosts"
	r := g.Check(cmd)
	if r.Behavior == Block {
		t.Errorf("benign multi-line command must not Block, reason: %s", r.Reason)
	}
}

func TestCommandGateQuotedSeparatorStillBlocks(t *testing.T) {
	g := NewCommandGate()
	r := g.Check(`rm -f "a;b" -r /etc`)
	if r.Behavior != Block {
		t.Errorf("quoted-separator rm -rf on /etc must Block, got %v (reason: %s)", r.Behavior, r.Reason)
	}

	r2 := g.Check(`rm -f a -r /etc`)
	if r2.Behavior != Block {
		t.Errorf("plain rm -rf /etc must Block, got %v", r2.Behavior)
	}

	r3 := g.Check(`rm -f "a;b"`)
	if r3.Behavior == Block {
		t.Errorf("rm -f of a quoted local file must not Block: %s", r3.Reason)
	}
}
