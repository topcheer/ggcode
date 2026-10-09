package agent

import "testing"

// shouldDeferCacheAwareCompact (r20 cache-aware compaction deferral) coverage.
func TestShouldDeferCacheAwareCompact(t *testing.T) {
	const window = 100000 // hard cap = 97000
	cases := []struct {
		name      string
		hitRatio  float64
		tokens    int
		ctxWindow int
		want      bool
	}{
		{"hot cache, below hard cap -> defer", 0.8, 92000, window, true},
		{"hot cache exactly 0.6 -> defer", 0.6, 92000, window, true},
		{"hot cache, at hard cap -> compact now", 0.8, 97500, window, false},
		{"hot cache, way over cap -> compact now", 0.9, 99999, window, false},
		{"cold cache -> compact on schedule", 0.1, 92000, window, false},
		{"no-cache provider (0) -> compact on schedule", 0, 92000, window, false},
		{"unknown window -> never defer", 0.9, 50, 0, false},
		{"empty window guard", 0.9, 50, -1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldDeferCacheAwareCompact(c.hitRatio, c.tokens, c.ctxWindow); got != c.want {
				t.Fatalf("shouldDeferCacheAwareCompact(%v, %d, %d) = %v, want %v", c.hitRatio, c.tokens, c.ctxWindow, got, c.want)
			}
		})
	}
}
