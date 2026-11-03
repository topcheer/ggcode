package knight

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

func TestBucketBudgetIsolationUnderDefaultConfig(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true}
	b := NewBudget(t.TempDir(), cfg)
	if got := b.DailyLimit(); got != defaultKnightDailyTokenBudget {
		t.Fatalf("NewBudget default config: DailyLimit() = %d, want %d", got, defaultKnightDailyTokenBudget)
	}

	bb := newBucketBudget(normalizeDailyTokenBudget(cfg))
	if bb.daily != defaultKnightDailyTokenBudget {
		t.Fatalf("bucket budget daily = %d, want %d", bb.daily, defaultKnightDailyTokenBudget)
	}
	evalCap := bb.bucketCap[BudgetBucketEval]
	analysisCap := bb.bucketCap[BudgetBucketAnalysis]
	if evalCap <= 0 || analysisCap <= 0 {
		t.Fatalf("bucket caps must be non-zero under default config, eval=%d analysis=%d", evalCap, analysisCap)
	}
	if analysisCap <= evalCap {
		t.Fatalf("analysis cap (%d) must exceed eval cap (%d) per design goal", analysisCap, evalCap)
	}

	now := time.Now()

	bb.record(BudgetBucketEval, evalCap, now)
	if bb.canSpend(BudgetBucketEval, now) {
		t.Fatalf("eval bucket must be blocked after spending its cap (%d)", evalCap)
	}

	if !bb.canSpend(BudgetBucketAnalysis, now) {
		t.Fatalf("analysis bucket must remain spendable after eval exhaustion")
	}
}

func TestKnightConstructionBudgetParity(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.KnightConfig
	}{
		{"unset default", config.KnightConfig{Enabled: true}},
		{"negative falls back", config.KnightConfig{Enabled: true, DailyTokenBudget: -42}},
		{"explicit positive", config.KnightConfig{Enabled: true, DailyTokenBudget: 1_000_000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := New(tc.cfg, t.TempDir(), t.TempDir(), nil)
			normalized := normalizeDailyTokenBudget(tc.cfg)
			if got := k.budget.DailyLimit(); got != normalized {
				t.Fatalf("budget.DailyLimit() = %d, want normalized %d", got, normalized)
			}
			if k.bucketBudget.daily != normalized {
				t.Fatalf("bucketBudget.daily = %d, want normalized %d", k.bucketBudget.daily, normalized)
			}
			for bucket, cap := range k.bucketBudget.bucketCap {
				if cap <= 0 {
					t.Fatalf("bucket %s cap = %d, want > 0", bucket, cap)
				}
			}
		})
	}
}

func TestCandidateQueueRemoveSanitizesNameAndSignalsMiss(t *testing.T) {
	debug.EnableForTest(t, "knight")
	queue := NewCandidateQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err := queue.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	stored := SkillCandidate{Name: "Build_Flow", Scope: "project", Score: 4.2, EvidenceCount: 3}
	if err := queue.Upsert(stored); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	raw := SkillCandidate{Name: "Build_Flow", Scope: "project"}
	if err := queue.Remove(raw); err != nil {
		t.Fatalf("Remove(raw name) error = %v", err)
	}
	items, err := queue.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected sanitized Remove to delete the stored candidate, got %+v", items)
	}

	missing := SkillCandidate{Name: "never-queued-candidate", Scope: "project"}
	if err := queue.Remove(missing); err != nil {
		t.Fatalf("Remove(missing) error = %v", err)
	}
	found := false
	for _, entry := range debug.RingHistory(200, "knight") {
		if strings.Contains(entry.Message, "no entry matched") && strings.Contains(entry.Message, "never-queued-candidate") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected debug log for Remove miss on never-queued-candidate")
	}
}
