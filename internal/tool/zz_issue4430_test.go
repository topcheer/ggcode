package tool

import "testing"

// #4430: heredocOpenerRe must not treat <<< (here-string) as a heredoc opener,
// and rmP's `~` branch must match `~/` and bare `~`.
func TestGateHereStringNotHeredoc(t *testing.T) {
	g := NewCommandGate()

	// here-string body line is a real command: catastrophic rm must hard-Block.
	got := g.Check("cat <<< EOF\nrm -rf /etc\nEOF")
	if got.Behavior != Block {
		t.Errorf("here-string rm -rf /etc: behavior=%v, want Block (got %+v)", got.Behavior, got)
	}

	// regression: a GENUINE heredoc body must stay inert (script-writing stays Ask, #814).
	got = g.Check("cat << EOF\nrm -rf /etc\nEOF")
	if got.Behavior == Block {
		t.Errorf("genuine heredoc body must not hard-Block: %+v", got)
	}

	// heredoc delimiter extraction helper.
	if _, ok := heredocDelimiter("cat << 'DELIM'"); !ok {
		t.Error("heredocDelimiter must accept genuine heredoc opener")
	}
	if hd, ok := heredocDelimiter("cat <<< EOF"); ok {
		t.Errorf("heredocDelimiter must reject here-string opener, got %q", hd)
	}
	if hd, ok := heredocDelimiter("cat <<<< X"); ok {
		t.Errorf("heredocDelimiter must reject multi-< run, got %q", hd)
	}
}

func TestGateRMHomeScatteredFlags(t *testing.T) {
	g := NewCommandGate()

	for _, cmd := range []string{
		"rm --recursive ~ --force", // was Allowed entirely (dead ~\b anchor)
		"rm -f ~ -r",               // was only Ask
		"rm -rf ~",                 // was only Ask
		"rm --force ~ --recursive", // path-between-flags permutation
		"rm -rf ~/",                // was already Block via literal ~/
		"rm -f /etc -r",            // regression: unrelated root still Block
	} {
		got := g.Check(cmd)
		if got.Behavior != Block {
			t.Errorf("Check(%q): behavior=%v, want Block (got %+v)", cmd, got.Behavior, got)
		}
	}

	// regression: benign homes under /tmp and per-dir deletes stay unblocked.
	for _, cmd := range []string{
		"rm -rf /tmp/build-cache",
		"rm file.txt",
		"rm -rf ./subdir",
	} {
		got := g.Check(cmd)
		if got.Behavior == Block {
			t.Errorf("Check(%q) must not hard-Block: %+v", cmd, got)
		}
	}
}
