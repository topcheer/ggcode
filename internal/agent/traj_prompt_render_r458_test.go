package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r458: learning store consumption probes (render for prompt injection).

func writeLearnings(t *testing.T, dir string, ls []trajectoryLearning) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, l := range ls {
		enc.Encode(l)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkLearning(ts time.Time, typ, cat, insight string) trajectoryLearning {
	return trajectoryLearning{Timestamp: ts, Type: typ, Category: cat, Insight: insight}
}

func TestRenderPromptSection_EmptyAndAbsent(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	t.Setenv("HOME", t.TempDir()) // r460: isolate from any real global store
	dir := t.TempDir()
	s := newTrajIntelState()
	if got := s.RenderPromptSection(dir); got != "" {
		t.Fatalf("absent store must render empty, got %q", got)
	}
	writeLearnings(t, dir, nil)
	s2 := newTrajIntelState()
	if got := s2.RenderPromptSection(dir); got != "" {
		t.Fatalf("empty store must render empty, got %q", got)
	}
}

func TestRenderPromptSection_RendersAndDedupes(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	t.Setenv("HOME", t.TempDir()) // r460: isolate from any real global store
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	writeLearnings(t, dir, []trajectoryLearning{
		mkLearning(base, "strategy", "over_exploration", "old insight"),
		mkLearning(base.Add(time.Minute), "strategy", "over_exploration", "new insight"),
		mkLearning(base.Add(2*time.Minute), "recovery", "retry_storm", "backoff then narrow scope"),
	})
	s := newTrajIntelState()
	got := s.RenderPromptSection(dir)
	if !strings.Contains(got, "new insight") {
		t.Fatalf("newest per-category must win: %q", got)
	}
	if strings.Contains(got, "old insight") {
		t.Fatalf("same category must dedupe to newest: %q", got)
	}
	if !strings.Contains(got, "[recovery] backoff") {
		t.Fatalf("recovery entry missing: %q", got)
	}
	if strings.Contains(got, base.Format(time.RFC3339)) {
		t.Fatalf("deterministic render: no timestamps allowed: %q", got)
	}
}

func TestRenderPromptSection_Budgets(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	t.Setenv("HOME", t.TempDir()) // r460: isolate from any real global store
	dir := t.TempDir()
	base := time.Now()
	var ls []trajectoryLearning
	for i := 0; i < 30; i++ {
		ls = append(ls, mkLearning(base.Add(time.Duration(i)*time.Second),
			[]string{"strategy", "recovery", "optimization", "teammate"}[i%4],
			"cat"+string(rune('a'+i%26))+"x"+string(rune('0'+i%10)), "insight "+strings.Repeat("z", 60)))
	}
	writeLearnings(t, dir, ls)
	s := newTrajIntelState()
	got := s.RenderPromptSection(dir)
	lines := strings.Count(got, "- [")
	if lines > trajPromptMaxEntries {
		t.Fatalf("entry budget blown: %d", lines)
	}
	if len(got) > trajPromptMaxChars+200 {
		t.Fatalf("char budget blown: %d", len(got))
	}
}

func TestRenderPromptSection_TeammateEntriesIncluded(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	t.Setenv("HOME", t.TempDir()) // r460: isolate from any real global store
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	writeLearnings(t, dir, []trajectoryLearning{
		mkLearning(base, "teammate", "teammate_experience", "teammate distilled: prefer worktree isolation"),
	})
	s := newTrajIntelState()
	got := s.RenderPromptSection(dir)
	if !strings.Contains(got, "worktree isolation") {
		t.Fatalf("teammate learnings must surface: %q", got)
	}
}
