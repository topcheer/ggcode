package knight

import (
	"strings"
	"sync"
	"time"
)

type budgetBucket string

const (
	BudgetBucketAnalysis    budgetBucket = "analysis"
	BudgetBucketEval        budgetBucket = "eval"
	BudgetBucketMaintenance budgetBucket = "maintenance"
	BudgetBucketProposal    budgetBucket = "proposal"
	BudgetBucketSkillTuning budgetBucket = "skill_tuning"
	BudgetBucketAdhoc       budgetBucket = "adhoc"
)

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

type bucketBudget struct {
	mu        sync.Mutex
	dayKey    string
	daily     int
	bucketCap map[budgetBucket]int
	used      map[budgetBucket]int
}

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

func classifyBucket(taskName string) budgetBucket {
	if b, ok := taskNameBucketMap[strings.TrimSpace(taskName)]; ok {
		return b
	}
	return BudgetBucketAdhoc
}

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
