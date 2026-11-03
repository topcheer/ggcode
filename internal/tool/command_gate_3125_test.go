package tool

import "testing"

func TestGateQuotedFlagVariantsNotBypassed(t *testing.T) {
	gate := NewCommandGate()
	cases := []string{
		`rm "-rf" /tmp/probe-3125`,
		`rm '-rf' /tmp/probe-3125`,
		`git push "-f" origin main`,
		`git push '--force' origin main`,
		`git reset "--hard"`,
		`git clean "-fd" .`,
	}
	for _, cmd := range cases {
		res := gate.Check(cmd)
		if res.Behavior == Allow {
			t.Errorf("quoted-flag variant bypassed gate: %q -> Allow", cmd)
		}
		if !gate.IsDestructive(cmd) {
			t.Errorf("IsDestructive missed quoted-flag variant: %q", cmd)
		}
	}
}

func TestGateQuotedFlagUnwrapKeepsEchoClean(t *testing.T) {
	gate := NewCommandGate()
	cases := []string{
		`echo "rm -rf /tmp/demo"`,
		`echo 'git reset --hard'`,
		`echo "done"`,
		`printf "shutdown scheduled"`,
	}
	for _, cmd := range cases {
		res := gate.Check(cmd)
		if res.Behavior == Block {
			t.Errorf("false Block on quoted argument text: %q", cmd)
		}

	}
}

func TestGateUnwrapSkipsHeredocBodies(t *testing.T) {
	cmd := "cat <<'EOF' > /tmp/x.sh\nrm -rf data\nEOF"
	if got := unwrapSimpleQuotedTokens(cmd); got != cmd {
		t.Errorf("heredoc body must stay verbatim, got %q", got)
	}

	gate := NewCommandGate()
	if res := gate.Check(cmd); res.Behavior == Block {
		t.Errorf("heredoc body false Block: %q", cmd)
	}
}

func TestGateMatchViewsDoubleObfuscation(t *testing.T) {
	gate := NewCommandGate()

	cmd := `rm "-rf" "a;b" /tmp/probe-3125`
	res := gate.Check(cmd)
	if res.Behavior == Allow {
		t.Errorf("double obfuscation bypassed gate: %q", cmd)
	}
	if !gate.IsDestructive(cmd) {
		t.Errorf("IsDestructive missed double obfuscation: %q", cmd)
	}
}

func TestUnwrapSimpleQuotedTokensBasics(t *testing.T) {
	cases := map[string]string{
		`rm "-rf" x`:         "rm -rf x",
		`chmod "777" f`:      "chmod 777 f",
		`echo "a b"`:         `echo "a b"`,
		`echo 'hello world'`: `echo 'hello world'`,
		`plain`:              "plain",
	}
	for in, want := range cases {
		if got := unwrapSimpleQuotedTokens(in); got != want {
			t.Errorf("unwrap(%q) = %q, want %q", in, got, want)
		}
	}
}
