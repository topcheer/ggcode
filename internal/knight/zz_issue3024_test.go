package knight

// Regression probe for #3024: the proposal id's nanosecond disambiguation
// suffix must never be truncated away. The old whole-id [:80] cut ate the
// suffix first for slugs longer than 53 chars, deterministically reviving
// the same-second same-title collision fixed by #1576-B (file overwrite +
// log collapse losing the first proposal).

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue3024_LongSlugKeepsDisambiguationSuffix: two long-title proposals
// written within the same second must get DIFFERENT ids (suffix preserved),
// and both .md files must survive.
func TestIssue3024_LongSlugKeepsDisambiguationSuffix(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)

	longTitle := strings.Repeat("refactor the module boundary ", 8) // slug >> 53 chars
	p1, err := k.writeProjectImprovementProposal("goal", "# "+longTitle+"\n## Summary\nfirst\n")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := k.writeProjectImprovementProposal("goal", "# "+longTitle+"\n## Summary\nsecond\n")
	if err != nil {
		t.Fatal(err)
	}
	if p1.ID == p2.ID {
		t.Fatalf("#3024: same-second long-title proposals collide: %q", p1.ID)
	}
	if len(p1.ID) > 80 {
		t.Fatalf("id must respect the 80-char cap, got %d (%q)", len(p1.ID), p1.ID)
	}
}

// TestIssue3024_LongSlugIDsSharePrefixButDifferInSuffix pins the shape: the
// timestamp+slug prefix may be shared, the tail differs (the suffix did the
// disambiguation, not a truncated slug byte).
func TestIssue3024_LongSlugIDsSharePrefixButDifferInSuffix(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)

	longTitle := strings.Repeat("boundary cleanup sweep ", 9)
	ids := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		p, err := k.writeProjectImprovementProposal("goal", "# "+longTitle+"\n## Summary\nbody\n")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	if ids[0] == ids[1] {
		t.Fatalf("ids must differ: %q", ids[0])
	}
	prefix := ids[0][:len(ids[0])-9]
	if !strings.HasPrefix(ids[1], prefix) {
		t.Fatalf("ids should share the timestamp+slug prefix, got %q vs %q", ids[0], ids[1])
	}
}

// TestIssue3024_ShortSlugUnchanged: short titles keep the historical id
// layout (timestamp + full slug + suffix).
func TestIssue3024_ShortSlugUnchanged(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)
	p, err := k.writeProjectImprovementProposal("goal", "# Speed up build\n## Summary\ncache\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.ID, "-speed-up-build-") {
		t.Fatalf("short slug must appear untruncated in id, got %q", p.ID)
	}
	if len(p.ID) > 80 {
		t.Fatalf("id over cap: %q", p.ID)
	}
}
