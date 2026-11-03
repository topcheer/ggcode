package knight

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue3024_LongSlugKeepsDisambiguationSuffix(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)

	longTitle := strings.Repeat("refactor the module boundary ", 8)
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
