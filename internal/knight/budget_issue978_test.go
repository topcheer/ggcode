package knight

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// TestBucketBudgetIsolationUnderDefaultConfig locks in issue #978 fix: under
// the default config (daily budget unset), NewBudget falls back to 5M and the
// bucket-level guardrail must derive its caps from that same normalized total
// instead of the raw 0 (which made every bucket unlimited and let eval starve
// analysis with no signal).
func TestBucketBudgetIsolationUnderDefaultConfig(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true} // DailyTokenBudget unset (0, not explicit)
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
	// Exhaust the eval bucket.
	bb.record(BudgetBucketEval, evalCap, now)
	if bb.canSpend(BudgetBucketEval, now) {
		t.Fatalf("eval bucket must be blocked after spending its cap (%d)", evalCap)
	}
	// Analysis bucket must stay isolated (this was silently broken before).
	if !bb.canSpend(BudgetBucketAnalysis, now) {
		t.Fatalf("analysis bucket must remain spendable after eval exhaustion")
	}
}

// TestKnightConstructionBudgetParity verifies Knight.New feeds the bucket
// guardrail and the total Budget from the same normalization path, so their
// daily totals agree for unset, negative, and explicit positive configs.
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

// TestCandidateQueueRemoveSanitizesNameAndSignalsMiss covers issue #978 part
// two: Remove must sanitize the candidate name the same way Upsert does, and
// a no-match removal must leave a debug log trail instead of being fully
// silent.
func TestCandidateQueueRemoveSanitizesNameAndSignalsMiss(t *testing.T) {
	debug.EnableForTest(t, "knight")
	queue := NewCandidateQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err := queue.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	// Upsert sanitizes "Build_Flow" -> "build-flow" before storing.
	stored := SkillCandidate{Name: "Build_Flow", Scope: "project", Score: 4.2, EvidenceCount: 3}
	if err := queue.Upsert(stored); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	// Remove with a raw unsanitized name must still target the stored key.
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

	// Removing a nonexistent candidate must not error, but must log the miss.
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
