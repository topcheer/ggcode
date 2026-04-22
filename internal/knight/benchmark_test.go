package knight

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// BenchmarkSkillIndexScan benchmarks the skill index scanning.
func BenchmarkSkillIndexScan(b *testing.B) {
	dir := b.TempDir()
	cfg := config.DefaultKnightConfig()
	k := New(cfg, dir, dir, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = k.index.Scan()
	}
}

// BenchmarkCollectProjectConventions benchmarks project convention collection.
func BenchmarkCollectProjectConventions(b *testing.B) {
	dir := b.TempDir()
	cfg := config.DefaultKnightConfig()
	k := New(cfg, dir, dir, nil)
	sa := NewSessionAnalyzer(k)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sa.CollectProjectConventions()
	}
}

// BenchmarkCandidateQueueUpsert benchmarks the candidate queue.
func BenchmarkCandidateQueueUpsert(b *testing.B) {
	dir := b.TempDir()
	q := NewCandidateQueue(dir)
	_ = q.EnsureDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidate := SkillCandidate{
			Name:  "bench-test",
			Score: 0.5,
		}
		_ = q.Upsert(candidate)
	}
}

// BenchmarkUsageTrackerRecord benchmarks usage recording.
func BenchmarkUsageTrackerRecord(b *testing.B) {
	dir := b.TempDir()
	ut := NewUsageTracker(dir)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ut.RecordUse("bench-skill")
	}
}

// BenchmarkUsageTrackerEffectiveness benchmarks effectiveness recording.
func BenchmarkUsageTrackerEffectiveness(b *testing.B) {
	dir := b.TempDir()
	ut := NewUsageTracker(dir)
	// Pre-populate
	for i := 0; i < 100; i++ {
		ut.RecordUse("bench-skill")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ut.RecordEffectiveness("bench-skill", i%5)
	}
}

// BenchmarkBudgetRecord benchmarks budget tracking.
func BenchmarkBudgetRecord(b *testing.B) {
	dir := b.TempDir()
	budget := NewBudget(dir, config.DefaultKnightConfig())
	budget.EnsureDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = budget.Record("bench-task", 100, 200)
	}
}

// BenchmarkValidateSkill benchmarks skill validation.
func BenchmarkValidateSkill(b *testing.B) {
	entry := &SkillEntry{
		Name:  "bench-skill",
		Scope: "project",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateSkill(entry)
	}
}
