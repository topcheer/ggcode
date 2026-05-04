package knight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestSelfReflectionRecommendsActionableGovernanceSteps(t *testing.T) {
	homeDir := t.TempDir()
	projDir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)

	writeActiveSkillFixture(t, homeDir, projDir, "global", "stale-global", "Old global guidance", time.Now().Add(-10*24*time.Hour),
		"# stale-global\n\n## When to Use\nUse for the ggcode CLI.\n\n## Steps\n1. Do the old thing.\n")
	writeActiveSkillFixture(t, homeDir, projDir, "project", "noisy-selection", "Noisy selection guidance", time.Now().Add(-10*24*time.Hour),
		"# noisy-selection\n\n## When to Use\nUse for every task.\n\n## Steps\n1. Always trigger.\n")
	k.index.Invalidate()

	for i := 0; i < knightPromptIgnoredThreshold; i++ {
		k.RecordSkillPromptExposure([]string{"global:stale-global", "project:noisy-selection"})
	}
	for i := 0; i < knightPromptOutcomeMinSamples; i++ {
		k.RecordPromptSkillOutcome([]string{"project:noisy-selection"}, false)
	}

	report, err := k.RunSelfReflection(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("RunSelfReflection() error = %v", err)
	}
	assertRecommendationPresent(t, report.Recommendations, "global:stale-global", "review-retire")
	assertRecommendationPresent(t, report.Recommendations, "project:noisy-selection", "tighten-trigger")
}

func TestRunGovernanceAuditSummarizesCountsAndOverlap(t *testing.T) {
	homeDir := t.TempDir()
	projDir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)

	writeActiveSkillFixture(t, homeDir, projDir, "global", "stale-global", "Old global guidance", time.Now().Add(-40*24*time.Hour),
		"# stale-global\n\n## When to Use\nUse for the ggcode CLI.\n\n## Steps\n1. Do the old thing.\n")
	writeActiveSkillFixture(t, homeDir, projDir, "project", "build-verify", "Build before push", time.Now().Add(-40*24*time.Hour),
		"# build-verify\n\n## When to Use\nBefore pushing changes.\n\n## Steps\n1. Run go build.\n2. Run go test ./...\n3. Never push without verification.\n")
	writeActiveSkillFixture(t, homeDir, projDir, "project", "build-verify-twice", "Build before push with confirmation", time.Now().Add(-40*24*time.Hour),
		"# build-verify-twice\n\n## When to Use\nBefore pushing changes.\n\n## Steps\n1. Run go build.\n2. Run go test ./...\n3. Never push without verification.\n")
	k.index.Invalidate()

	for i := 0; i < knightPromptIgnoredThreshold; i++ {
		k.RecordSkillPromptExposure([]string{"global:stale-global"})
	}
	if err := k.SetSkillFrozen("project:build-verify", true); err != nil {
		t.Fatalf("SetSkillFrozen() error = %v", err)
	}

	audit, err := k.RunGovernanceAudit(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("RunGovernanceAudit() error = %v", err)
	}
	if audit.ActiveSkills != 3 || audit.GlobalSkills != 1 || audit.ProjectSkills != 2 {
		t.Fatalf("unexpected audit counts: %+v", audit)
	}
	if audit.FrozenSkills != 1 {
		t.Fatalf("expected 1 frozen skill, got %+v", audit)
	}
	if !containsString(audit.StaleGlobalSkills, "global:stale-global") {
		t.Fatalf("expected stale global skill in audit, got %+v", audit.StaleGlobalSkills)
	}
	assertRecommendationPresent(t, audit.Recommendations, "project:build-verify <-> project:build-verify-twice", "merge-review")
}

func TestCandidateQueuePriorityRewardsAgeAndPersistence(t *testing.T) {
	now := time.Now()
	oldCandidate := SkillCandidate{
		Name:            "old-candidate",
		Scope:           "project",
		Score:           3.0,
		EvidenceCount:   3,
		Category:        "convention",
		FirstQueuedAt:   now.Add(-14 * 24 * time.Hour),
		QueueTouchCount: 5,
	}
	newCandidate := SkillCandidate{
		Name:            "new-candidate",
		Scope:           "project",
		Score:           3.2,
		EvidenceCount:   3,
		Category:        "convention",
		FirstQueuedAt:   now.Add(-24 * time.Hour),
		QueueTouchCount: 1,
	}
	oldPriority, _ := candidateQueuePriority(oldCandidate, now)
	newPriority, _ := candidateQueuePriority(newCandidate, now)
	if oldPriority <= newPriority {
		t.Fatalf("expected old candidate priority %.1f to exceed new candidate %.1f", oldPriority, newPriority)
	}
}

func TestSortSkillCandidatesAtDiversifiesCategories(t *testing.T) {
	now := time.Now()
	items := []SkillCandidate{
		{
			Name:            "correction-top",
			Scope:           "project",
			Category:        "correction",
			Score:           4.3,
			EvidenceCount:   3,
			FirstQueuedAt:   now.Add(-48 * time.Hour),
			QueueTouchCount: 2,
		},
		{
			Name:            "correction-second",
			Scope:           "project",
			Category:        "correction",
			Score:           4.2,
			EvidenceCount:   3,
			FirstQueuedAt:   now.Add(-48 * time.Hour),
			QueueTouchCount: 2,
		},
		{
			Name:            "convention-third",
			Scope:           "project",
			Category:        "convention",
			Score:           4.1,
			EvidenceCount:   3,
			FirstQueuedAt:   now.Add(-48 * time.Hour),
			QueueTouchCount: 2,
		},
	}
	sortSkillCandidatesAt(items, now)
	if items[0].Name != "correction-top" {
		t.Fatalf("expected strongest candidate first, got %+v", items)
	}
	if items[1].Category != "convention" {
		t.Fatalf("expected second slot diversified by category, got %+v", items)
	}
}

func TestCandidateQueueUpsertTracksQueueTouches(t *testing.T) {
	queue := NewCandidateQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err := queue.Upsert(SkillCandidate{Name: "build-flow", Scope: "project", Score: 4.0, EvidenceCount: 3}); err != nil {
		t.Fatalf("first Upsert() error = %v", err)
	}
	if err := queue.Upsert(SkillCandidate{Name: "build-flow", Scope: "project", Score: 4.5, EvidenceCount: 4}); err != nil {
		t.Fatalf("second Upsert() error = %v", err)
	}
	items, err := queue.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 queue item, got %+v", items)
	}
	if items[0].QueueTouchCount < 2 {
		t.Fatalf("expected touch count to increase, got %+v", items[0])
	}
	if items[0].FirstQueuedAt.IsZero() || items[0].LastQueuedAt.IsZero() {
		t.Fatalf("expected queue timestamps to be populated, got %+v", items[0])
	}
	if items[0].QueuePriority <= 0 || strings.TrimSpace(items[0].QueuePriorityReason) == "" {
		t.Fatalf("expected priority metadata, got %+v", items[0])
	}
}

func writeActiveSkillFixture(t *testing.T, homeDir, projDir, scope, name, desc string, createdAt time.Time, body string) {
	t.Helper()
	baseDir := filepath.Join(projDir, ".ggcode", "skills")
	if scope == "global" {
		baseDir = filepath.Join(homeDir, ".ggcode", "skills")
	}
	skillDir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", skillDir, err)
	}
	content := fmt.Sprintf(`---
name: %q
description: %q
scope: %q
created_by: "knight"
created_at: %q
---

%s
`, name, desc, scope, createdAt.Format(time.RFC3339), body)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}

func assertRecommendationPresent(t *testing.T, recs []SkillActionRecommendation, ref, action string) {
	t.Helper()
	for _, rec := range recs {
		if rec.Ref == ref && rec.Action == action {
			return
		}
	}
	t.Fatalf("expected recommendation %s %s, got %+v", action, ref, recs)
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
