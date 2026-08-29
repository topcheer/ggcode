package memory

// Regression tests for GitHub issues #1278-#1280: dedupKeyFor over-stripping
// caused permanent GC deletion of distinct memories; sanitizeKey dash-fold
// collisions silently overwrote files; contradiction self-update skip never
// matched hash-suffixed keys.

import (
	"os"
	"path/filepath"
	"testing"
)

// --- #1278: dedupKeyFor must not collapse distinct semantic segments ---

func TestDedupKeyDistinctSemanticSegments(t *testing.T) {
	cases := []struct {
		a, b string // distinct keys that MUST NOT share a dedup key
	}{
		{"perf-gen2-analysis", "perf-gen3-analysis"},
		{"port-8080-config", "port-9090-config"},
		{"web2-notes", "web3-notes"},
		{"app1-design", "app2-design"},
		{"ver2-findings", "ver3-findings"},
	}
	for _, c := range cases {
		if dedupKeyFor(c.a) == dedupKeyFor(c.b) {
			t.Fatalf("#1278: %q and %q collapsed to dedup key %q - GC would delete the loser",
				c.a, c.b, dedupKeyFor(c.a))
		}
	}
}

func TestDedupKeyDateAndVersionStrippingStillWorks(t *testing.T) {
	// The feature dedupKeyFor exists for: date- and version-suffixed
	// variants of the same topic still group together.
	same := [][2]string{
		{"competitor-analysis-2026-07-13-r3", "competitor-analysis-2026-07-20-r4"},
		{"competitor-analysis-jul6", "competitor-analysis-jul13"},
		{"competitor-analysis-2026-07-r2", "competitor-analysis-2026-08-r3"},
	}
	for _, p := range same {
		if dedupKeyFor(p[0]) != dedupKeyFor(p[1]) {
			t.Fatalf("expected %q and %q to share a dedup key, got %q vs %q",
				p[0], p[1], dedupKeyFor(p[0]), dedupKeyFor(p[1]))
		}
	}
	// Year must still count as a date when a plausible MM follows.
	if dedupKeyFor("topic-2026-07-details") != dedupKeyFor("topic-2026-08-details") {
		t.Fatalf("year-month variants must group: %q vs %q",
			dedupKeyFor("topic-2026-07-details"), dedupKeyFor("topic-2026-08-details"))
	}
}

// --- #1279: sanitizeKey dash folding must not collide distinct keys ---

func TestDisambiguateKeyDashFoldCollision(t *testing.T) {
	cases := [][2]string{
		{"a-b", "a--b"},
		{"a--b", "a---b"},
		{"build", "-build-"},
	}
	for _, c := range cases {
		ka := disambiguateKey(c[0], sanitizeKey(c[0]))
		kb := disambiguateKey(c[1], sanitizeKey(c[1]))
		if ka == kb {
			t.Fatalf("#1279: %q and %q still map to the same file %q (silent overwrite)", c[0], c[1], ka)
		}
	}
	// Plain safe keys keep their readable, suffix-free form (no regression
	// of the #775 readable-name goal).
	for _, k := range []string{"build-process", "api_gotcha", "release-100"} {
		if got := disambiguateKey(k, sanitizeKey(k)); got != k {
			t.Fatalf("plain key %q must stay readable, got %q", k, got)
		}
	}
}

func TestSaveMemoryDistinctDashKeysBothSurvive(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	if err := am.SaveMemory("a-b", "content of a-b"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("a--b", "content of a--b"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, disambiguateKey("a-b", sanitizeKey("a-b"))+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "content of a-b" {
		t.Fatalf("#1279: second save overwrote the first (got %q)", string(first))
	}
}

// --- #1280: contradiction self-update skip must match hash-suffixed keys ---

func TestContradictionSelfUpdateCJKKeyNoSelfConflict(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	// A CJK key gets a hash suffix on disk; the old skip compared against
	// the sanitized (""→"untitled-less") form and never matched, so saving a
	// value that contradicts the file's own previous value warned
	// "contradicts existing memory <self>".
	if err := am.SaveMemory("构建流程", "old value A"); err != nil {
		t.Fatal(err)
	}
	cc := am.CheckContradiction("构建流程", "new value B")
	selfKey := disambiguateKey("构建流程", sanitizeKey("构建流程"))
	for _, c := range cc.Conflicts {
		if c.ExistingKey == selfKey {
			t.Fatalf("#1280: self-update flagged as conflict with itself: %+v", c)
		}
	}
}
