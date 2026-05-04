package knight

import (
	"strings"
	"sync"
	"time"
)

// budgetBucket identifies how a Knight LLM call is being spent. Each bucket
// has its own daily cap (a fraction of the total budget) so a noisy eval
// loop cannot starve analysis or proposals.
type budgetBucket string

const (
	BudgetBucketAnalysis    budgetBucket = "analysis"
	BudgetBucketEval        budgetBucket = "eval"
	BudgetBucketMaintenance budgetBucket = "maintenance"
	BudgetBucketProposal    budgetBucket = "proposal"
	BudgetBucketSkillTuning budgetBucket = "skill_tuning"
	BudgetBucketAdhoc       budgetBucket = "adhoc"
)

// taskNameBucketMap maps the task names used by RunTask/RunTaskWithTurns to
// budget buckets. Anything unmapped lands in adhoc.
var taskNameBucketMap = map[string]budgetBucket{
	"skill-generation":             BudgetBucketAnalysis,
	"skill-auto-promote-eval":      BudgetBucketEval,
	"skill-ab-replay":              BudgetBucketEval,
	"skill-patch":                  BudgetBucketSkillTuning,
	"skill-prompt-tuning":          BudgetBucketSkillTuning,
	"project-improvement-proposal": BudgetBucketProposal,
	"nightly-regression-audit":     BudgetBucketMaintenance,
	"nightly-doc-audit":            BudgetBucketMaintenance,
	"manual-task":                  BudgetBucketAdhoc,
}

// bucketBudget tracks per-bucket spend within a single rolling day. Daily
// rollover is keyed by the date string ("2006-01-02") of the first record
// for that day. The store is intentionally in-memory: persistent budget
// accounting still goes through Budget; this is a guardrail.
type bucketBudget struct {
	mu        sync.Mutex
	dayKey    string
	daily     int
	bucketCap map[budgetBucket]int
	used      map[budgetBucket]int
}

// newBucketBudget configures bucket fractions of the daily total. Defaults are
// chosen so eval cannot crowd out analysis even when LLM noise spikes.
func newBucketBudget(dailyTotal int) *bucketBudget {
	if dailyTotal < 0 {
		dailyTotal = 0
	}
	caps := map[budgetBucket]int{}
	if dailyTotal > 0 {
		caps[BudgetBucketAnalysis] = max1(dailyTotal * 50 / 100)
		caps[BudgetBucketEval] = max1(dailyTotal * 25 / 100)
		caps[BudgetBucketMaintenance] = max1(dailyTotal * 10 / 100)
		caps[BudgetBucketProposal] = max1(dailyTotal * 10 / 100)
		caps[BudgetBucketSkillTuning] = max1(dailyTotal * 10 / 100)
		caps[BudgetBucketAdhoc] = max1(dailyTotal * 50 / 100)
	}
	return &bucketBudget{
		daily:     dailyTotal,
		bucketCap: caps,
		used:      map[budgetBucket]int{},
	}
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

// classify returns the budget bucket for a task name.
func classifyBucket(taskName string) budgetBucket {
	if b, ok := taskNameBucketMap[strings.TrimSpace(taskName)]; ok {
		return b
	}
	return BudgetBucketAdhoc
}

// canSpend reports whether a fresh task in the given bucket may start.
// Unlimited daily budget always returns true.
func (b *bucketBudget) canSpend(bucket budgetBucket, now time.Time) bool {
	if b == nil || b.daily <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rolloverLocked(now)
	cap := b.bucketCap[bucket]
	if cap <= 0 {
		return true
	}
	return b.used[bucket] < cap
}

// record adds tokens to the per-bucket counter and rolls over on a new day.
func (b *bucketBudget) record(bucket budgetBucket, tokens int, now time.Time) {
	if b == nil || tokens <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rolloverLocked(now)
	b.used[bucket] += tokens
}

func (b *bucketBudget) rolloverLocked(now time.Time) {
	day := now.Format("2006-01-02")
	if b.dayKey == day {
		return
	}
	b.dayKey = day
	b.used = map[budgetBucket]int{}
}

// snapshot returns a copy of the current per-bucket usage and caps for the
// rolling day; useful for /knight status diagnostics.
func (b *bucketBudget) snapshot(now time.Time) map[budgetBucket][2]int {
	out := map[budgetBucket][2]int{}
	if b == nil {
		return out
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rolloverLocked(now)
	for bucket, cap := range b.bucketCap {
		out[bucket] = [2]int{b.used[bucket], cap}
	}
	return out
}
