package permission

import "testing"

func TestUserFriendlyPatternsCachedHits(t *testing.T) {
	pats := []string{"(?i)^git .*", "^rm\\ -rf"}
	a := userFriendlyPatternsCached(pats)
	b := userFriendlyPatternsCached(pats)
	if len(a) != len(pats) {
		t.Fatalf("len mismatch: %d != %d", len(a), len(pats))
	}
	if &a[0] != &b[0] {
		t.Fatalf("expected memoized same backing array")
	}
	if a[0] != "git *" || a[1] != "rm -rf" {
		t.Fatalf("unexpected render: %q", a)
	}
	if got := userFriendlyPatternsCached(nil); got != nil {
		t.Fatalf("nil passthrough: %q", got)
	}
}
