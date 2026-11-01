package agent

import "testing"

// zz_issue4877_test.go pins the fix for #4877: the arg-complexity hard token
// "git push --force" was substring-matched, so (a) the equivalent short forms
// (-f, +refspec) scored ZERO on the hard tier, and (b) the safer
// --force-with-lease variant scored the SAME +8 as bare force - contradicting
// git_destructive_detect's warning-level tiering for the lease form.
//
// Fix shape: token-level pushForceScore - bare/short/+refspec force → hard 8;
// --force-with-lease → mid 4 (below the threshold-8 gate alone).

func TestPushForceScoreTiers(t *testing.T) {
	cases := []struct {
		cmd      string
		wantHard int
		wantMid  int
	}{
		{"git push origin main", 0, 0},
		{"git push --force origin main", 8, 0},
		{"git push -f origin main", 8, 0},
		{"git push -fv origin main", 8, 0},
		{"git push origin +main", 8, 0},
		{"git push --force-with-lease origin main", 0, 4},
		{"git push --force-with-lease=abc123 origin main", 0, 4},
		{"git push", 0, 0},
		{"git status", 0, 0},
		// A later segment's tokens must not misattribute to an earlier push.
		{"git push origin main && git checkout --force other", 0, 0},
	}
	for _, c := range cases {
		h, m := pushForceScore(c.cmd)
		if h != c.wantHard || m != c.wantMid {
			t.Errorf("pushForceScore(%q) = (%d, %d), want (%d, %d)", c.cmd, h, m, c.wantHard, c.wantMid)
		}
	}
}

func TestScoreShellComplexityForceTiers(t *testing.T) {
	// The #4877 headline assertions at the scoreShellComplexity level:
	// every true force spelling must fire the threshold-8 gate; the lease
	// variant must NOT fire it alone (mid weight only) yet score above a
	// plain push.
	plain := scoreShellComplexity("git push origin main")
	if plain >= 8 {
		t.Fatalf("plain push must not fire the gate: score=%d", plain)
	}
	for _, force := range []string{
		"git push --force origin main",
		"git push -f origin main",
		"git push -fv origin main",
		"git push origin +main",
	} {
		if got := scoreShellComplexity(force); got < 8 {
			t.Errorf("#4877 regression: %q scores %d (<8) - true force form invisible to the gate", force, got)
		}
	}
	lease := scoreShellComplexity("git push --force-with-lease origin main")
	if lease >= 8 {
		t.Errorf("#4877 regression: --force-with-lease fires the hard gate (score=%d) - contradicts git_destructive_detect's warning tiering for the safe form", lease)
	}
	if lease <= plain {
		t.Errorf("lease variant (%d) should score above a plain push (%d) via the mid weight", lease, plain)
	}
}
