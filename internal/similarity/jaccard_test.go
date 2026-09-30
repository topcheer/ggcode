package similarity

import "testing"

func TestJaccard(t *testing.T) {
	s := func(xs ...string) map[string]struct{} {
		m := make(map[string]struct{}, len(xs))
		for _, x := range xs {
			m[x] = struct{}{}
		}
		return m
	}
	b := func(xs ...string) map[string]bool {
		m := make(map[string]bool, len(xs))
		for _, x := range xs {
			m[x] = true
		}
		return m
	}

	third := 1.0 / 3.0
	if got := Jaccard(s("a", "b"), s("b", "c")); got != third {
		t.Fatalf("Jaccard({a,b},{b,c}) = %v, want %v", got, third)
	}
	if got := Jaccard(s("a"), s("a")); got != 1 {
		t.Fatalf("identity = %v, want 1", got)
	}
	if got := Jaccard(s("a"), s("b")); got != 0 {
		t.Fatalf("disjoint = %v, want 0", got)
	}
	if got := Jaccard(s(), s("a")); got != 0 {
		t.Fatalf("empty vs non-empty = %v, want 0", got)
	}
	if got := Jaccard(s(), s()); got != 0 {
		t.Fatalf("both empty = %v, want 0", got)
	}
	if got := Jaccard(b("a", "b"), b("b", "c")); got != third {
		t.Fatalf("map[string]bool variant = %v, want %v", got, third)
	}
}
