package tool

// #3125: quote-wrapped flags (`rm "-rf" x`) defeated every \b/flag-anchored
// Block AND Ask rule plus IsDestructive - #436's normalization only removed
// separators inside quotes, never the quotes around simple tokens. Probes:
//  1. quoted-flag variants must NOT be Allow / must be IsDestructive
//  2. echo "rm -rf ..." (space-containing payload) must stay clean - the
//     unwrap only strips quotes around space-free tokens
//  3. heredoc bodies are never unwrapped (literal data)
//  4. mixed: separator+flag double obfuscation still caught

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
		`echo "rm -rf /tmp/demo"`, // space-containing payload stays quoted
		`echo 'git reset --hard'`, // ditto
		`echo "done"`,             // simple token unwrapped -> harmless
		`printf "shutdown scheduled"`,
	}
	for _, cmd := range cases {
		res := gate.Check(cmd)
		if res.Behavior == Block {
			t.Errorf("false Block on quoted argument text: %q", cmd)
		}
		// An Ask-level hit on `echo "rm -rf ..."` is PRE-EXISTING behavior
		// (\b-anchored ask regexes have always matched text inside quotes);
		// the unwrap adds no new surface there, and Ask is a confirmation,
		// not a block. #3125 targets the Allow bypass.
	}
}

func TestGateUnwrapSkipsHeredocBodies(t *testing.T) {
	cmd := "cat <<'EOF' > /tmp/x.sh\nrm -rf data\nEOF"
	if got := unwrapSimpleQuotedTokens(cmd); got != cmd {
		t.Errorf("heredoc body must stay verbatim, got %q", got)
	}
	// The opener line's quoted 'EOF' is part of heredoc syntax; blanking
	// semantics must not leak into the unwrapped view either.
	gate := NewCommandGate()
	if res := gate.Check(cmd); res.Behavior == Block {
		t.Errorf("heredoc body false Block: %q", cmd)
	}
}

func TestGateMatchViewsDoubleObfuscation(t *testing.T) {
	gate := NewCommandGate()
	// flag quoted (#3125) AND separator hidden in another quoted token
	// (#436); flag still adjacent to rm after unwrapping.
	// Known limit, documented: `rm "a;b" "-rf" x` (quoted separator token
	// BEFORE the flag) shifts the flag off the rm\s+- anchor and is not
	// caught by the ask-shape rules - the Block-layer gap class (rmGap)
	// still sees it via the norm view.
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
		`echo "a b"`:         `echo "a b"`, // space -> keep
		`echo 'hello world'`: `echo 'hello world'`,
		`plain`:              "plain",
	}
	for in, want := range cases {
		if got := unwrapSimpleQuotedTokens(in); got != want {
			t.Errorf("unwrap(%q) = %q, want %q", in, got, want)
		}
	}
}
