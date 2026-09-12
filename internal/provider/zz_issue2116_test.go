package provider

// #2116 regression: tiered context probing billed ~tier tokens of real
// input per attempt with no cumulative cap - a small-window model behind
// a gateway whose overflow errors carry no exact number descended the
// whole ladder (1M+512K+256K ≈ 1.8M tokens) silently. The descent is now
// capped by probeTokenBudget.

import "testing"

func TestTierFitsBudget(t *testing.T) {
	cases := []struct {
		name        string
		spent, next int
		want        bool
	}{
		{"fresh 1M tier fits", 0, 1_000_000, true},
		{"1M spent, 512K next overflows", 1_000_000, 512_000, false},
		{"1M spent, 256K next fits", 1_000_000, 256_000, true},
		{"exactly at budget fits", probeTokenBudget - 64_000, 64_000, true},
		{"one over budget fails", probeTokenBudget - 64_000 + 1, 64_000, false},
	}
	for _, c := range cases {
		if got := tierFitsBudget(c.spent, c.next, probeTokenBudget); got != c.want {
			t.Errorf("%s: tierFitsBudget(%d, %d) = %v, want %v", c.name, c.spent, c.next, got, c.want)
		}
	}
}

// The ladder, walked with the budget, must stop before the cumulative
// billed tokens exceed the cap: the worst case becomes 1M (first tier)
// instead of 1.8M.
func TestTierLadderBudgetWalk(t *testing.T) {
	spent := 0
	attempted := 0
	for _, tier := range probeTiers {
		if !tierFitsBudget(spent, tier, probeTokenBudget) {
			break
		}
		spent += tier // billed whether the tier succeeds or fails
		attempted++
	}
	if spent > probeTokenBudget {
		t.Fatalf("walk spent %d tokens, over budget %d", spent, probeTokenBudget)
	}
	if attempted < 1 {
		t.Fatal("first tier must always fit an empty budget")
	}
	// The exact walk: 1M fits, 512K does not (1.512M > 1.5M).
	if attempted != 1 || spent != 1_000_000 {
		t.Fatalf("expected the walk to stop after the 1M tier (spent=%d attempted=%d), got spent=%d attempted=%d", 1_000_000, 1, spent, attempted)
	}
}
