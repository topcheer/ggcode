package agent

import "testing"

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
