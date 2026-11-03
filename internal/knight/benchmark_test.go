package knight

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func BenchmarkSkillIndexScan(b *testing.B) {
	dir := b.TempDir()
	cfg := config.DefaultKnightConfig()
	k := New(cfg, dir, dir, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = k.index.Scan()
	}
}

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

func BenchmarkUsageTrackerRecord(b *testing.B) {
	dir := b.TempDir()
	ut := NewUsageTracker(dir)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ut.RecordUse("bench-skill")
	}
}

func BenchmarkUsageTrackerEffectiveness(b *testing.B) {
	dir := b.TempDir()
	ut := NewUsageTracker(dir)

	for i := 0; i < 100; i++ {
		ut.RecordUse("bench-skill")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ut.RecordEffectiveness("bench-skill", i%5)
	}
}

func BenchmarkBudgetRecord(b *testing.B) {
	dir := b.TempDir()
	budget := NewBudget(dir, config.DefaultKnightConfig())
	budget.EnsureDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = budget.Record("bench-task", 100, 200)
	}
}

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
