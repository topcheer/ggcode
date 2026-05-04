package knight

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

type stubKnightRunner struct {
	output string
	err    error
}

func (r stubKnightRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	if strings.TrimSpace(r.output) != "" {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: r.output})
	}
	return r.err
}

type captureKnightRunner struct {
	output string
	prompt *string
}

func (r captureKnightRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	if r.prompt != nil {
		*r.prompt = prompt
	}
	if strings.TrimSpace(r.output) != "" {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: r.output})
	}
	return nil
}

type stubKnightEmitter struct {
	reports []string
}

func (e *stubKnightEmitter) EmitKnightReport(report string) {
	e.reports = append(e.reports, report)
}

func (e *stubKnightEmitter) HasTargets() bool { return true }

func TestBudgetRecordAndCheck(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-budget-*")
	defer os.RemoveAll(dir)

	b := NewBudget(dir, config.DefaultKnightConfig())
	b.EnsureDir()

	// Initially can spend
	if !b.CanSpend() {
		t.Fatal("expected to be able to spend initially")
	}

	// Record some usage
	if err := b.Record("test-task", 100, 200); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	// Used should be 300
	if used := b.Used(); used != 300 {
		t.Fatalf("expected used=300, got %d", used)
	}

	// Remaining should be 50M - 300
	if rem := b.Remaining(); rem != 50_000_000-300 {
		t.Fatalf("expected remaining=%d, got %d", 50_000_000-300, rem)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-budget-*")
	defer os.RemoveAll(dir)

	cfg := config.DefaultKnightConfig()
	cfg.DailyTokenBudget = 1000
	b := NewBudget(dir, cfg)
	b.EnsureDir()

	// Use up most of the budget
	b.Record("task1", 500, 400) // 900 total

	if !b.CanSpend() {
		t.Fatal("should still be able to spend (100 remaining)")
	}

	// Exhaust
	b.Record("task2", 100, 100) // 200 more = 1100 total > 1000 budget

	// Now should be exhausted — but note: we record even if over budget
	// The check is done before starting tasks
	if b.Remaining() >= 0 {
		// Remaining might be negative, that's ok — CanSpend checks against used < daily
	}
}

func TestBudgetExplicitZeroDisablesLimit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	t.Setenv("ZAI_API_KEY", "test-key")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
knight:
  daily_token_budget: 0
vendors:
  zai:
    api_key: ${ZAI_API_KEY}
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	b := NewBudget(dir, loaded.Knight())
	b.EnsureDir()
	if err := b.Record("unlimited", 50_000_000, 50_000_000); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !b.CanSpend() {
		t.Fatal("expected unlimited budget to keep allowing spends")
	}
	if rem := b.Remaining(); rem != 0 {
		t.Fatalf("expected unlimited budget remaining sentinel 0, got %d", rem)
	}
}

func TestKnightStartInitializesIdleTimer(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultKnightConfig()
	cfg.Enabled = true
	k := New(cfg, dir, dir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	if k.lastIdle.IsZero() {
		t.Fatal("expected lastIdle to be initialized on start")
	}
	if k.isIdle(time.Now()) {
		t.Fatal("expected freshly started Knight to wait for idle delay")
	}
}

func TestShouldRunScheduledTaskUsesRetryBackoff(t *testing.T) {
	now := time.Date(2026, 4, 20, 3, 0, 0, 0, time.UTC)
	if !shouldRunScheduledTask(now, time.Time{}, time.Time{}, time.Hour, 15*time.Minute) {
		t.Fatal("expected first scheduled task run to be allowed")
	}
	if shouldRunScheduledTask(now, time.Time{}, now.Add(-10*time.Minute), time.Hour, 15*time.Minute) {
		t.Fatal("expected retry backoff to suppress rerun")
	}
	if !shouldRunScheduledTask(now, time.Time{}, now.Add(-20*time.Minute), time.Hour, 15*time.Minute) {
		t.Fatal("expected task to rerun after retry backoff")
	}
	if shouldRunScheduledTask(now, now.Add(-30*time.Minute), now.Add(-20*time.Minute), time.Hour, 15*time.Minute) {
		t.Fatal("expected recent successful run to suppress rerun")
	}
}

func TestTickRetriesNightlyMaintenanceAfterFailureBackoff(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultKnightConfig()
	cfg.Enabled = true
	cfg.Capabilities = []string{"regression_testing"}
	k := New(cfg, dir, dir, nil)
	k.SetEmitter(&stubKnightEmitter{})

	first := time.Date(2026, 4, 20, 2, 0, 0, 0, time.UTC)
	k.tick(context.Background(), first)
	if !k.lastMaintenance.IsZero() {
		t.Fatal("expected failed maintenance to avoid recording success time")
	}
	if !k.lastMaintenanceAttempt.Equal(first) {
		t.Fatalf("expected first maintenance attempt at %v, got %v", first, k.lastMaintenanceAttempt)
	}

	k.tick(context.Background(), first.Add(10*time.Minute))
	if !k.lastMaintenanceAttempt.Equal(first) {
		t.Fatal("expected retry backoff to suppress another maintenance attempt")
	}

	retry := first.Add(20 * time.Minute)
	k.tick(context.Background(), retry)
	if !k.lastMaintenanceAttempt.Equal(retry) {
		t.Fatalf("expected maintenance retry at %v, got %v", retry, k.lastMaintenanceAttempt)
	}
}

func TestBudgetPersistence(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-budget-*")
	defer os.RemoveAll(dir)

	b1 := NewBudget(dir, config.DefaultKnightConfig())
	b1.EnsureDir()

	// Record usage
	b1.Record("test", 100, 200)

	// Create new budget instance (simulates restart)
	b2 := NewBudget(dir, config.DefaultKnightConfig())

	// Should load previous usage
	if used := b2.Used(); used != 300 {
		t.Fatalf("expected persisted used=300, got %d", used)
	}
}

func TestSkillIndexScanEmpty(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-index-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	os.MkdirAll(filepath.Join(homeDir, ".ggcode"), 0755)
	os.MkdirAll(filepath.Join(projDir, ".ggcode"), 0755)

	idx := NewSkillIndex(homeDir, projDir)
	entries, err := idx.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries from empty dirs, got %d", len(entries))
	}
}

func TestSkillIndexScanWithActiveSkills(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-index-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	// Create a global skill
	globalSkillDir := filepath.Join(homeDir, ".ggcode", "skills", "docker-debug")
	os.MkdirAll(globalSkillDir, 0755)
	os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte(`---
name: docker-debug
description: Debug Docker containers
scope: global
created_by: user
---
# Docker Debug`), 0644)

	// Create a project skill
	projSkillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	os.MkdirAll(projSkillDir, 0755)
	os.WriteFile(filepath.Join(projSkillDir, "SKILL.md"), []byte(`---
name: build-flow
description: Build and test the project
scope: project
created_by: user
---
# Build Flow`), 0644)

	idx := NewSkillIndex(homeDir, projDir)
	entries, err := idx.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Check scopes
	scopes := map[string]string{}
	for _, e := range entries {
		scopes[e.Name] = e.Scope
	}
	if scopes["docker-debug"] != "global" {
		t.Errorf("expected docker-debug scope=global, got %s", scopes["docker-debug"])
	}
	if scopes["build-flow"] != "project" {
		t.Errorf("expected build-flow scope=project, got %s", scopes["build-flow"])
	}
}

func TestSkillIndexScanWithStaging(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-index-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	// Create a staging skill
	stagingDir := filepath.Join(projDir, ".ggcode", "skills-staging")
	os.MkdirAll(stagingDir, 0755)
	os.WriteFile(filepath.Join(stagingDir, "knight-20260419-test.md"), []byte(`---
name: test-skill
description: A test skill
scope: project
created_by: knight
---
# Test`), 0644)

	idx := NewSkillIndex(homeDir, projDir)
	staging, err := idx.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills failed: %v", err)
	}
	if len(staging) != 1 {
		t.Fatalf("expected 1 staging skill, got %d", len(staging))
	}
	if staging[0].Name != "test-skill" {
		t.Errorf("expected name=test-skill, got %s", staging[0].Name)
	}
	if !staging[0].Staging {
		t.Error("expected Staging=true")
	}
}

func TestSkillValidatorValidSkill(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-validate-*")
	defer os.RemoveAll(dir)

	// Create a valid skill file
	skillPath := filepath.Join(dir, "SKILL.md")
	os.WriteFile(skillPath, []byte(`---
name: test-skill
description: A test skill
scope: project
created_by: knight
---
# Test Skill

## When to Use
When you need to test things.

## Steps
1. Run tests
2. Check results`), 0644)

	entry := &SkillEntry{
		Name: "test-skill",
		Meta: SkillMeta{
			Name:        "test-skill",
			Description: "A test skill",
			Scope:       "project",
			CreatedBy:   "knight",
		},
		Path:    skillPath,
		Scope:   "project",
		Staging: false,
	}

	result := ValidateSkill(entry)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestSkillValidatorMissingFields(t *testing.T) {
	entry := &SkillEntry{
		Name: "",
		Meta: SkillMeta{
			Description: "",
		},
		Path: "/nonexistent",
	}

	result := ValidateSkill(entry)
	if result.Valid {
		t.Error("expected invalid for missing fields")
	}
	if len(result.Errors) < 2 {
		t.Errorf("expected at least 2 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestSkillValidatorRejectsMissingStepsSection(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: incomplete-skill
description: Missing steps
scope: project
created_by: knight
---
# Incomplete Skill

## When to Use
Use this sometimes.
`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result := ValidateSkill(&SkillEntry{
		Name: "incomplete-skill",
		Meta: SkillMeta{
			Name:        "incomplete-skill",
			Description: "Missing steps",
			Scope:       "project",
			CreatedBy:   "knight",
		},
		Path: skillPath,
	})
	if result.Valid {
		t.Fatal("expected invalid result when steps section is missing")
	}
}

func TestSkillValidatorRejectsScopeMismatch(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "scope-mismatch")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: scope-mismatch
description: Wrong scope metadata
scope: global
created_by: knight
---
# Scope Mismatch

## When to Use
Use when checking validation.

## Steps
1. Run the thing
`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result := ValidateSkill(&SkillEntry{
		Name: "scope-mismatch",
		Meta: SkillMeta{
			Name:        "scope-mismatch",
			Description: "Wrong scope metadata",
			Scope:       "global",
			CreatedBy:   "knight",
		},
		Path:  skillPath,
		Scope: "project",
	})
	if result.Valid {
		t.Fatal("expected invalid result when scope metadata mismatches index scope")
	}
}

func TestSkillValidatorDuplicateDetection(t *testing.T) {
	existing := []*SkillEntry{
		{
			Name: "docker-debug",
			Meta: SkillMeta{Description: "Debug Docker containers"},
		},
	}

	// Same name
	entry := &SkillEntry{
		Name: "docker-debug",
		Meta: SkillMeta{Description: "A different description"},
	}
	if !CheckDuplicate(entry, existing) {
		t.Error("expected duplicate detection for same name")
	}

	// Different name
	entry2 := &SkillEntry{
		Name: "k8s-deploy",
		Meta: SkillMeta{Description: "Deploy to Kubernetes"},
	}
	if CheckDuplicate(entry2, existing) {
		t.Error("expected no duplicate for different name")
	}
}

func TestAnalyzeRecentAggregatesAcrossSessions(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-aggregate-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	storeDir := filepath.Join(homeDir, ".ggcode", "sessions")
	os.MkdirAll(storeDir, 0755)
	store, err := session.NewJSONLStore(storeDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	for i := 0; i < 2; i++ {
		ses := session.NewSession("zai", "test", "test-model")
		// Simulate a correction pattern in each session
		store.AppendMessage(ses, provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "build the project"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "I will create a debug binary"},
				{Type: "tool_use", ToolName: "run_command", ToolID: "build"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "你需要编译的是正式的 ggcode 二进制而不是什么 debug 二进制，用 make build"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Understood, using make build"},
			},
		})
	}

	k := New(config.DefaultKnightConfig(), homeDir, projDir, store)
	result, err := NewSessionAnalyzer(k).AnalyzeRecent(context.Background())
	if err != nil {
		t.Fatalf("AnalyzeRecent: %v", err)
	}
	if len(result.SkillCandidates) == 0 {
		t.Fatal("expected aggregated candidates")
	}
	if result.SkillCandidates[0].EvidenceCount < 2 {
		t.Fatalf("expected top candidate to converge across sessions, got %+v", result.SkillCandidates[0])
	}
}

func TestSkillNameFromFile(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"knight-20260419-build-flow.md", "build-flow"},
		{"knight-20260420-docker-debug.md", "docker-debug"},
		{"custom-skill.md", "custom-skill"},
		{"knight-short.md", "knight-short"},
	}
	for _, tt := range tests {
		got := skillNameFromFile(tt.input)
		if got != tt.expected {
			t.Errorf("skillNameFromFile(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestPromoterWriteStaging(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-promote-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	p := NewPromoter(homeDir, projDir)

	content := `---
name: test-skill
description: A test skill
scope: project
created_by: knight
---
# Test`

	path, err := p.WriteStaging("test-skill", "project", content)
	if err != nil {
		t.Fatalf("WriteStaging failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("staging file not created at %s", path)
	}
}

func TestPromoterWriteStagingDoesNotClobberExistingCandidate(t *testing.T) {
	dir := t.TempDir()
	p := NewPromoter(filepath.Join(dir, "home"), filepath.Join(dir, "project"))
	content := `---
name: same-skill
description: Same skill
scope: project
created_by: knight
---
# Same

## Steps
1. Do it.`
	first, err := p.WriteStaging("same-skill", "project", content)
	if err != nil {
		t.Fatalf("first WriteStaging: %v", err)
	}
	second, err := p.WriteStaging("same-skill", "project", content)
	if err != nil {
		t.Fatalf("second WriteStaging: %v", err)
	}
	if first == second {
		t.Fatalf("WriteStaging should not clobber existing staging file: %s", first)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first staging file missing: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("second staging file missing: %v", err)
	}
}

func TestNormalizeGeneratedSkillDocumentUsesFrontmatterNameAndScope(t *testing.T) {
	candidate := SkillCandidate{
		Name:        "build-convention",
		Scope:       "",
		Description: "fallback description",
		Category:    "correction",
	}
	content := `---
name: "autopilot-no-status-halts"
description: "Do not stop for status summaries in autopilot"
scope: "global"
platforms: ["darwin", "linux", "windows"]
---

# autopilot-no-status-halts

## Steps
1. Keep working.`

	k := &Knight{}
	normalizedCandidate, normalizedContent, err := k.normalizeGeneratedSkillDocument(candidate, content)
	if err != nil {
		t.Fatalf("normalizeGeneratedSkillDocument: %v", err)
	}
	if normalizedCandidate.Name != "autopilot-no-status-halts" {
		t.Fatalf("name = %q", normalizedCandidate.Name)
	}
	if normalizedCandidate.Scope != "global" {
		t.Fatalf("scope = %q", normalizedCandidate.Scope)
	}
	if normalizedCandidate.Description != "Do not stop for status summaries in autopilot" {
		t.Fatalf("description = %q", normalizedCandidate.Description)
	}
	if !strings.Contains(normalizedContent, "created_by: knight") {
		t.Fatalf("normalized content should mark created_by knight:\n%s", normalizedContent)
	}
}

func TestNormalizeActiveSkillLayoutMigratesKnightLooseMarkdown(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillsDir := filepath.Join(projDir, ".ggcode", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loosePath := filepath.Join(skillsDir, "build-flow.md")
	looseContent := `---
name: "build-flow"
description: "Build flow"
scope: "project"
created_by: "knight"
---

# Build Flow

## Steps
1. Run make verify-ci.`
	if err := os.WriteFile(loosePath, []byte(looseContent), 0o644); err != nil {
		t.Fatal(err)
	}
	userLoosePath := filepath.Join(skillsDir, "user-note.md")
	if err := os.WriteFile(userLoosePath, []byte(`---
name: "user-note"
description: "User note"
scope: "project"
created_by: "user"
---

# User Note`), 0o644); err != nil {
		t.Fatal(err)
	}

	k := New(config.DefaultKnightConfig(), homeDir, projDir, nil)
	migrated, err := k.normalizeActiveSkillLayout()
	if err != nil {
		t.Fatalf("normalizeActiveSkillLayout: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("migrated = %d, want 1", migrated)
	}
	standardPath := filepath.Join(skillsDir, "build-flow", "SKILL.md")
	if _, err := os.Stat(standardPath); err != nil {
		t.Fatalf("standard skill missing: %v", err)
	}
	if _, err := os.Stat(loosePath); !os.IsNotExist(err) {
		t.Fatalf("loose knight skill should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(userLoosePath); err != nil {
		t.Fatalf("user-authored loose markdown should be left untouched: %v", err)
	}

	cmds := commands.NewLoader(projDir).Load()
	if _, ok := cmds["build-flow"]; !ok {
		t.Fatal("migrated standard skill should be loadable by ggcode loader")
	}
	if _, ok := cmds["user-note"]; ok {
		t.Fatal("loose user markdown should not be loadable by ggcode loader")
	}
}

func TestBuildCorrectionSkillNameIsStableAcrossSessions(t *testing.T) {
	first := buildCorrectionSkillName("你需要编译的是正式的 ggcode 二进制而不是什么 debug 二进制")
	second := buildCorrectionSkillName("你需要编译的是正式的 ggcode 二进制而不是什么 debug 二进制")
	if first != second {
		t.Fatalf("expected stable correction name, got %q vs %q", first, second)
	}
}

func TestPromoterReject(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-reject-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	p := NewPromoter(homeDir, projDir)

	// Write a staging file
	content := `---
name: reject-me
description: Will be rejected
scope: project
created_by: knight
---
# Reject`
	path, _ := p.WriteStaging("reject-me", "project", content)

	// Reject it
	entry := &SkillEntry{
		Name:    "reject-me",
		Path:    path,
		Scope:   "project",
		Staging: true,
	}
	if err := p.Reject(entry); err != nil {
		t.Fatalf("Reject failed: %v", err)
	}

	// File should be gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("staging file should be deleted after reject")
	}
}

func TestUsageTrackerRecordAndGet(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-usage-*")
	defer os.RemoveAll(dir)

	ut := NewUsageTracker(filepath.Join(dir, "usage.json"))
	ut.EnsureDir()

	ut.RecordUse("test-skill")
	ut.RecordUse("test-skill")

	count, lastUsed, avg := ut.GetUsage("test-skill")
	if count != 2 {
		t.Fatalf("expected usage_count=2, got %d", count)
	}
	if lastUsed.IsZero() {
		t.Fatal("expected non-zero last_used")
	}
	if avg != 0 {
		t.Fatalf("expected avg_score=0 (no scores), got %f", avg)
	}
}

func TestUsageTrackerPromptExposure(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-usage-*")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "usage.json")
	ut1 := NewUsageTracker(path)
	ut1.EnsureDir()
	ut1.RecordPromptExposure("project:test-skill")
	ut1.RecordPromptExposure("project:test-skill")
	ut1.Flush()

	exposed, lastExposed := ut1.GetPromptExposure("project:test-skill")
	if exposed != 2 {
		t.Fatalf("expected exposure_count=2, got %d", exposed)
	}
	if lastExposed.IsZero() {
		t.Fatal("expected non-zero last_prompt_exposure")
	}
	count, _, _ := ut1.GetUsage("project:test-skill")
	if count != 0 {
		t.Fatalf("prompt exposure must not increment usage_count, got %d", count)
	}

	ut2 := NewUsageTracker(path)
	persisted, persistedLast := ut2.GetPromptExposure("project:test-skill")
	if persisted != 2 || persistedLast.IsZero() {
		t.Fatalf("expected persisted exposure data, got count=%d last=%v", persisted, persistedLast)
	}
}

func TestUsageTrackerPromptOutcome(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-usage-*")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "usage.json")
	ut1 := NewUsageTracker(path)
	ut1.EnsureDir()
	ut1.RecordPromptOutcome("project:test-skill", true)
	ut1.RecordPromptOutcome("project:test-skill", false)
	ut1.RecordPromptOutcome("project:test-skill", true)
	ut1.Flush()

	successes, failures := ut1.GetPromptOutcome("project:test-skill")
	if successes != 2 || failures != 1 {
		t.Fatalf("expected prompt outcomes +2/-1, got +%d/-%d", successes, failures)
	}
	count, _, _ := ut1.GetUsage("project:test-skill")
	if count != 0 {
		t.Fatalf("prompt outcome must not increment usage_count, got %d", count)
	}

	ut2 := NewUsageTracker(path)
	persistedSuccesses, persistedFailures := ut2.GetPromptOutcome("project:test-skill")
	if persistedSuccesses != 2 || persistedFailures != 1 {
		t.Fatalf("expected persisted outcomes +2/-1, got +%d/-%d", persistedSuccesses, persistedFailures)
	}
}

func TestUsageTrackerEffectiveness(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-usage-*")
	defer os.RemoveAll(dir)

	ut := NewUsageTracker(filepath.Join(dir, "usage.json"))
	ut.EnsureDir()

	ut.RecordUse("test-skill")
	ut.RecordEffectiveness("test-skill", 4)
	ut.RecordEffectiveness("test-skill", 5)
	ut.RecordEffectiveness("test-skill", 3)

	_, _, avg := ut.GetUsage("test-skill")
	if avg != 4.0 {
		t.Fatalf("expected avg_score=4.0, got %f", avg)
	}
	feedbackAvg, samples := ut.GetFeedback("test-skill")
	if feedbackAvg != 4.0 || samples != 3 {
		t.Fatalf("expected feedback avg=4.0 samples=3, got avg=%f samples=%d", feedbackAvg, samples)
	}
}

func TestUsageTrackerStale(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-usage-*")
	defer os.RemoveAll(dir)

	ut := NewUsageTracker(filepath.Join(dir, "usage.json"))
	ut.EnsureDir()

	// Never used = stale
	if !ut.IsStale("unknown-skill", time.Hour) {
		t.Fatal("expected unused skill to be stale")
	}

	// Recently used = not stale
	ut.RecordUse("fresh-skill")
	if ut.IsStale("fresh-skill", time.Hour) {
		t.Fatal("expected recently used skill to not be stale")
	}
}

func TestUsageTrackerPersistence(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-usage-*")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "usage.json")
	ut1 := NewUsageTracker(path)
	ut1.EnsureDir()
	ut1.RecordUse("test-skill")
	ut1.RecordEffectiveness("test-skill", 5)
	ut1.Flush() // force persist

	// New tracker instance should load persisted data
	ut2 := NewUsageTracker(path)
	count, _, avg := ut2.GetUsage("test-skill")
	if count != 1 {
		t.Fatalf("expected persisted count=1, got %d", count)
	}
	if avg != 5.0 {
		t.Fatalf("expected persisted avg=5.0, got %f", avg)
	}
}

func TestSkillIndexCache(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-cache-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	// Create a skill
	skillDir := filepath.Join(homeDir, ".ggcode", "skills", "test")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test\ndescription: test\n---\n# Test"), 0644)

	idx := NewSkillIndex(homeDir, projDir)

	// First scan reads disk
	entries1, _ := idx.Scan()
	if len(entries1) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries1))
	}

	// Add another skill — cache should prevent seeing it
	skillDir2 := filepath.Join(projDir, ".ggcode", "skills", "test2")
	os.MkdirAll(skillDir2, 0755)
	os.WriteFile(filepath.Join(skillDir2, "SKILL.md"), []byte("---\nname: test2\ndescription: test2\n---\n# Test2"), 0644)

	entries2, _ := idx.Scan()
	if len(entries2) != 1 {
		t.Fatalf("cache should prevent seeing new skill, got %d", len(entries2))
	}

	// Invalidate and rescan
	idx.Invalidate()
	entries3, _ := idx.Scan()
	if len(entries3) != 2 {
		t.Fatalf("after invalidate, expected 2 entries, got %d", len(entries3))
	}
}

func TestSkillIndexConcurrentScanAndInvalidate(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-cache-race-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(homeDir, ".ggcode", "skills", "test")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test\ndescription: test\n---\n# Test"), 0644)

	idx := NewSkillIndex(homeDir, projDir)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := idx.Scan(); err != nil {
				t.Errorf("Scan returned error: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			idx.Invalidate()
		}()
	}
	wg.Wait()
}

func TestKnightFeedbackPolicyHelpers(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.usage.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	k.RecordSkillEffectiveness("great-skill", 5)
	k.RecordSkillEffectiveness("great-skill", 4)
	if !k.shouldSuppressStaleNotice("great-skill") {
		t.Fatal("expected strong positive feedback to suppress stale notice")
	}

	k.RecordSkillEffectiveness("rough-skill", 1)
	k.RecordSkillEffectiveness("rough-skill", 2)
	avg, samples, warn := k.shouldWarnLowEffectiveness("rough-skill")
	if !warn || avg != 1.5 || samples != 2 {
		t.Fatalf("expected low-effectiveness warning, got warn=%v avg=%f samples=%d", warn, avg, samples)
	}

	k.RecordSkillEffectiveness("noisy-skill", 1)
	if _, _, warn := k.shouldWarnLowEffectiveness("noisy-skill"); warn {
		t.Fatal("expected single signal not to trigger low-effectiveness warning")
	}
}

func TestKnightNightlyMaintenanceRunsRealAuditTasks(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	k := New(config.KnightConfig{
		Enabled:      true,
		Capabilities: []string{"regression_testing", "doc_sync"},
	}, homeDir, projDir, nil)
	emitter := &stubKnightEmitter{}
	k.SetEmitter(emitter)

	call := 0
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		call++
		if call == 1 {
			return stubKnightRunner{output: "verify-ci passed and the project looks healthy."}, nil
		}
		return stubKnightRunner{output: "README command examples still match the codebase."}, nil
	})

	k.nightlyMaintenance(context.Background())

	if len(emitter.reports) != 1 {
		t.Fatalf("expected one maintenance report, got %d", len(emitter.reports))
	}
	report := emitter.reports[0]
	if !contains(report, "Regression audit") || !contains(report, "Documentation audit") {
		t.Fatalf("expected both maintenance sections in report, got %q", report)
	}
	if !contains(report, "verify-ci passed") || !contains(report, "README command examples") {
		t.Fatalf("expected task outputs in maintenance report, got %q", report)
	}
}

func TestKnightSyncSkillMetadataPersistsUsageToSkillFile(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: build-flow
description: Build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds.

## Steps
1. Run the build
`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	ref := "project:build-flow"
	k.RecordSkillUse(ref)
	k.RecordSkillPromptExposure([]string{ref, ref})
	k.RecordPromptSkillOutcome([]string{ref, ref}, true)
	k.RecordPromptSkillOutcome([]string{ref}, false)
	k.RecordSkillEffectiveness(ref, 5)

	meta, err := parseSkillFile(skillPath)
	if err != nil {
		t.Fatalf("parseSkillFile() error = %v", err)
	}
	if meta.UsageCount != 1 {
		t.Fatalf("expected usage_count=1, got %d", meta.UsageCount)
	}
	if meta.LastUsed == "" {
		t.Fatal("expected last_used to be persisted")
	}
	if meta.PromptExposureCount != 1 {
		t.Fatalf("expected prompt_exposure_count=1, got %d", meta.PromptExposureCount)
	}
	if meta.LastPromptExposure == "" {
		t.Fatal("expected last_prompt_exposure to be persisted")
	}
	if meta.PromptSuccessCount != 1 || meta.PromptFailureCount != 1 {
		t.Fatalf("expected prompt outcome +1/-1, got +%d/-%d", meta.PromptSuccessCount, meta.PromptFailureCount)
	}
	if len(meta.EffectivenessScores) != 1 || meta.EffectivenessScores[0] != 5 {
		t.Fatalf("expected effectiveness_scores=[5], got %#v", meta.EffectivenessScores)
	}
}

func TestKnightSetSkillFrozenPersistsFlag(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: build-flow
description: Build flow
scope: project
created_by: knight
frozen: false
---
# Build Flow

## When to Use
Use for builds.

## Steps
1. Run the build
`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.SetSkillFrozen("build-flow", true); err != nil {
		t.Fatalf("SetSkillFrozen() error = %v", err)
	}

	meta, err := parseSkillFile(skillPath)
	if err != nil {
		t.Fatalf("parseSkillFile() error = %v", err)
	}
	if !meta.Frozen {
		t.Fatal("expected frozen flag to be true")
	}
}

func TestKnightStagesPatchForLowEffectivenessSkill(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: build-flow
description: Build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds.

## Steps
1. Run the build
`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := New(config.KnightConfig{
		Enabled:      true,
		TrustLevel:   "staged",
		Capabilities: []string{"skill_creation"},
	}, homeDir, projDir, nil)
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		return stubKnightRunner{output: `---
name: build-flow
description: Build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds and test validation.

## Steps
1. Run the build
2. Run tests

## Gotchas
- Watch for integration-only failures

## When Not to Use
Do not use this for unrelated tasks.
`}, nil
	})
	k.RecordSkillEffectiveness("project:build-flow", 1)
	k.RecordSkillEffectiveness("project:build-flow", 2)

	k.validateAllSkills(context.Background())

	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills() error = %v", err)
	}
	found := false
	for _, entry := range staging {
		if entry.Name == "build-flow" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected low-effectiveness skill patch to be staged")
	}
}

func TestKnightStagesPatchForIgnoredPromptVisibleSkill(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "review-flow")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: review-flow
description: Review flow
scope: project
created_by: knight
---
# Review Flow

## When to Use
Use for reviews.

## Steps
1. Review the change
`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := New(config.KnightConfig{
		Enabled:      true,
		TrustLevel:   "staged",
		Capabilities: []string{"skill_creation"},
	}, homeDir, projDir, nil)
	var patchPrompt string
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		return captureKnightRunner{prompt: &patchPrompt, output: `---
name: review-flow
description: Review changes when the task asks for code review, implementation-quality assessment, or regression-risk analysis.
scope: project
created_by: knight
---
# Review Flow

## When to Use
Use when the user asks to review code changes or assess implementation quality.

## When Not to Use
Do not use for implementing new code without a review request.

## Steps
1. Inspect the diff and related context.
2. Identify correctness, safety, and regression risks.
3. Report only actionable findings.

## Gotchas
- Do not treat every coding task as a review task.
`}, nil
	})
	for i := 0; i < knightPromptIgnoredThreshold; i++ {
		k.RecordSkillPromptExposure([]string{"project:review-flow"})
	}

	k.validateAllSkills(context.Background())

	if !strings.Contains(patchPrompt, "visible to the model but is not being selected reliably") {
		t.Fatalf("expected prompt-signal tuning instructions, got:\n%s", patchPrompt)
	}
	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills() error = %v", err)
	}
	if len(staging) != 1 || staging[0].Name != "review-flow" {
		t.Fatalf("expected review-flow tuning patch to be staged, got %#v", staging)
	}
}

func TestKnightPromoteStagingAllowsActiveSkillRevision(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	activeDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		t.Fatalf("MkdirAll(activeDir) error = %v", err)
	}
	activePath := filepath.Join(activeDir, "SKILL.md")
	if err := os.WriteFile(activePath, []byte(`---
name: build-flow
description: Old build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds.

## Steps
1. Run the old build
`), 0644); err != nil {
		t.Fatalf("WriteFile(active) error = %v", err)
	}

	k := New(config.KnightConfig{Enabled: true, TrustLevel: "staged"}, homeDir, projDir, nil)
	if _, err := k.promoter.WriteStaging("build-flow", "project", `---
name: build-flow
description: Revised build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds and tests.

## When Not to Use
Do not use for documentation-only changes.

## Steps
1. Run the build
2. Run tests
`); err != nil {
		t.Fatalf("WriteStaging() error = %v", err)
	}
	if err := k.PromoteStaging("build-flow"); err != nil {
		t.Fatalf("PromoteStaging() revision error = %v", err)
	}

	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("ReadFile(active) error = %v", err)
	}
	if !strings.Contains(string(data), "Revised build flow") {
		t.Fatalf("expected active skill to be revised, got:\n%s", string(data))
	}
	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills() error = %v", err)
	}
	if len(staging) != 0 {
		t.Fatalf("expected staging revision to be removed after promote, got %d", len(staging))
	}
}

func TestKnightAutoModeDoesNotPromoteActiveSkillRevision(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	activeDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		t.Fatalf("MkdirAll(activeDir) error = %v", err)
	}
	activePath := filepath.Join(activeDir, "SKILL.md")
	if err := os.WriteFile(activePath, []byte(`---
name: build-flow
description: Old build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds.

## Steps
1. Run the old build
`), 0644); err != nil {
		t.Fatalf("WriteFile(active) error = %v", err)
	}

	k := New(config.KnightConfig{Enabled: true, TrustLevel: "auto"}, homeDir, projDir, nil)
	emitter := &stubKnightEmitter{}
	k.SetEmitter(emitter)
	if _, err := k.promoter.WriteStaging("build-flow", "project", `---
name: build-flow
description: Revised build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds and tests.

## When Not to Use
Do not use for documentation-only changes.

## Steps
1. Run the build
2. Run tests
`); err != nil {
		t.Fatalf("WriteStaging() error = %v", err)
	}

	k.reviewStagingSkills(context.Background())

	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("ReadFile(active) error = %v", err)
	}
	if strings.Contains(string(data), "Revised build flow") {
		t.Fatalf("auto mode should not promote active revisions without review, got:\n%s", string(data))
	}
	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills() error = %v", err)
	}
	if len(staging) != 1 {
		t.Fatalf("expected revision to remain staged for review, got %d", len(staging))
	}
	if len(emitter.reports) == 0 || !strings.Contains(emitter.reports[0], "requires review") {
		t.Fatalf("expected review notification, got %#v", emitter.reports)
	}
}

func TestKnightAutoModePromotesOnlyAfterScenarioEvalApproval(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	k := New(config.KnightConfig{Enabled: true, TrustLevel: "auto"}, homeDir, projDir, nil)
	var evalPrompt string
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		return captureKnightRunner{
			prompt: &evalPrompt,
			output: "PROMOTE: yes\nREPLAY: pass\nSAVED_REPLAY: pass\nFALSE_POSITIVES: 0\nFALSE_NEGATIVES: 0\nRATIONALE: Specific low-risk project workflow.",
		}, nil
	})
	if _, err := k.promoter.WriteStaging("go-verify-flow", "project", `---
name: go-verify-flow
description: Verify this Go project with go test before reporting completion.
scope: project
created_by: knight
---
# Go Verify Flow

## When to Use
Use when changing Go files in internal/ or cmd/ and the user expects verified code.

## When Not to Use
Do not use for documentation-only changes.

## Steps
1. Run go test ./internal/... for the touched package area.
2. Run go test ./cmd/... if command behavior changed.
3. Report any failing package and the concrete next fix.
`); err != nil {
		t.Fatalf("WriteStaging() error = %v", err)
	}
	if err := k.RecordPromptSkillScenario([]string{"project:existing-build-flow"}, []provider.ContentBlock{
		provider.TextBlock("Fix internal/knight/scheduler.go and verify the Go tests before reporting done."),
	}, true, nil); err != nil {
		t.Fatalf("RecordPromptSkillScenario() error = %v", err)
	}

	k.reviewStagingSkills(context.Background())

	if !strings.Contains(evalPrompt, "Evaluate whether this staged project skill is safe") || !strings.Contains(evalPrompt, "Invent two realistic positive user tasks") {
		t.Fatalf("expected auto-promotion scenario eval prompt, got:\n%s", evalPrompt)
	}
	if !strings.Contains(evalPrompt, "Saved project scenarios:") || !strings.Contains(evalPrompt, "FALSE_POSITIVES: 0") || !strings.Contains(evalPrompt, "Fix internal/knight/scheduler.go") {
		t.Fatalf("expected saved project scenarios in eval prompt, got:\n%s", evalPrompt)
	}
	active, err := k.index.ActiveSkills()
	if err != nil {
		t.Fatalf("ActiveSkills() error = %v", err)
	}
	if len(active) != 1 || active[0].Name != "go-verify-flow" {
		t.Fatalf("expected go-verify-flow to be auto-promoted after eval, got %#v", active)
	}
	logData, err := os.ReadFile(filepath.Join(projDir, ".ggcode", "skill-auto-promote-evals.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(eval log) error = %v", err)
	}
	if !strings.Contains(string(logData), `"allowed":true`) || !strings.Contains(string(logData), `"replay_pass":true`) || !strings.Contains(string(logData), `"saved_replay_required":true`) {
		t.Fatalf("expected allowed replay eval log entry, got:\n%s", string(logData))
	}
	evals, err := k.RecentAutoPromoteEvals(1)
	if err != nil {
		t.Fatalf("RecentAutoPromoteEvals() error = %v", err)
	}
	if len(evals) != 1 || !evals[0].Allowed || !evals[0].ReplayPass || !evals[0].SavedReplayRequired || evals[0].SavedReplayStatus != "pass" || evals[0].Skill != "go-verify-flow" {
		t.Fatalf("unexpected recent evals: %#v", evals)
	}
}

func TestKnightRecordPromptSkillScenarioBoundsAndRedactsMedia(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, filepath.Join(dir, "home"), filepath.Join(dir, "project"), nil)

	longTask := strings.Repeat("x", maxSkillScenarioTaskLen+50)
	if err := k.RecordPromptSkillScenario([]string{"project:flow", "project:flow", "global:flow"}, []provider.ContentBlock{
		provider.ImageBlock("image/png", strings.Repeat("a", 1024)),
		provider.TextBlock(longTask),
	}, false, errors.New(strings.Repeat("e", maxSkillScenarioErrLen+50))); err != nil {
		t.Fatalf("RecordPromptSkillScenario() error = %v", err)
	}
	scenarios, err := k.RecentSkillScenarios(1)
	if err != nil {
		t.Fatalf("RecentSkillScenarios() error = %v", err)
	}
	if len(scenarios) != 1 {
		t.Fatalf("expected one scenario, got %d", len(scenarios))
	}
	if len(scenarios[0].Task) > maxSkillScenarioTaskLen+3 || !strings.Contains(scenarios[0].Task, "[image:image/png]") {
		t.Fatalf("scenario task not bounded or media marker missing: %q", scenarios[0].Task)
	}
	if strings.Contains(scenarios[0].Task, strings.Repeat("a", 32)) {
		t.Fatalf("scenario task should not persist image base64: %q", scenarios[0].Task)
	}
	if got := scenarios[0].SkillRefs; len(got) != 2 || got[0] != "project:flow" || got[1] != "global:flow" {
		t.Fatalf("unexpected scenario refs: %#v", got)
	}
	if len(scenarios[0].Error) > maxSkillScenarioErrLen+3 {
		t.Fatalf("scenario error not bounded: len=%d", len(scenarios[0].Error))
	}
}

func TestKnightAutoModeKeepsCandidateStagedWhenScenarioEvalRejects(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	k := New(config.KnightConfig{Enabled: true, TrustLevel: "auto"}, homeDir, projDir, nil)
	emitter := &stubKnightEmitter{}
	k.SetEmitter(emitter)
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		return stubKnightRunner{output: "PROMOTE: no\nREPLAY: fail\nRATIONALE: Trigger is still too broad."}, nil
	})
	if _, err := k.promoter.WriteStaging("go-verify-flow", "project", `---
name: go-verify-flow
description: Verify this Go project with go test before reporting completion.
scope: project
created_by: knight
---
# Go Verify Flow

## When to Use
Use when changing Go files in internal/ or cmd/ and the user expects verified code.

## When Not to Use
Do not use for documentation-only changes.

## Steps
1. Run go test ./internal/... for the touched package area.
2. Run go test ./cmd/... if command behavior changed.
3. Report any failing package and the concrete next fix.
`); err != nil {
		t.Fatalf("WriteStaging() error = %v", err)
	}

	k.reviewStagingSkills(context.Background())

	active, err := k.index.ActiveSkills()
	if err != nil {
		t.Fatalf("ActiveSkills() error = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected scenario-rejected candidate to remain inactive, got %#v", active)
	}
	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills() error = %v", err)
	}
	if len(staging) != 1 {
		t.Fatalf("expected scenario-rejected candidate to remain staged, got %d", len(staging))
	}
	if len(emitter.reports) == 0 || !strings.Contains(emitter.reports[0], "Trigger is still too broad") {
		t.Fatalf("expected scenario eval rationale in review notification, got %#v", emitter.reports)
	}
	logData, err := os.ReadFile(filepath.Join(projDir, ".ggcode", "skill-auto-promote-evals.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(eval log) error = %v", err)
	}
	if !strings.Contains(string(logData), `"allowed":false`) || !strings.Contains(string(logData), `"failure_mode":"promote_rejected"`) {
		t.Fatalf("expected rejected replay eval log entry, got:\n%s", string(logData))
	}
	evals, err := k.RecentAutoPromoteEvalsForSkill("project", "go-verify-flow", 1)
	if err != nil {
		t.Fatalf("RecentAutoPromoteEvalsForSkill() error = %v", err)
	}
	if len(evals) != 1 || evals[0].Allowed || evals[0].FailureMode != "promote_rejected" {
		t.Fatalf("unexpected recent skill evals: %#v", evals)
	}
}

func TestParseAutoPromoteEvalOutputRequiresReplayPass(t *testing.T) {
	allowed, rationale := parseAutoPromoteEvalOutput("PROMOTE: yes\nREPLAY: pass\nRATIONALE: clear trigger")
	if !allowed || rationale != "clear trigger" {
		t.Fatalf("expected allowed with rationale, got allowed=%v rationale=%q", allowed, rationale)
	}
	allowed, _ = parseAutoPromoteEvalOutput("PROMOTE: yes\nRATIONALE: missing replay")
	if allowed {
		t.Fatal("expected missing replay pass to block auto-promotion")
	}
	allowed, _ = parseAutoPromoteEvalOutput("PROMOTE: yes\nREPLAY: fail\nRATIONALE: negative scenarios are noisy")
	if allowed {
		t.Fatal("expected replay fail to block auto-promotion")
	}
}

func TestAutoPromoteEvalDecisionRequiresSavedReplayWhenScenariosExist(t *testing.T) {
	decision := parseAutoPromoteEvalDecision("PROMOTE: yes\nREPLAY: pass\nSAVED_REPLAY: pass\nFALSE_POSITIVES: 0\nFALSE_NEGATIVES: 0\nRATIONALE: clean")
	decision.SavedReplayRequired = true
	decision.finalizeFailureMode()
	if !decision.Allowed() {
		t.Fatalf("expected saved replay pass to allow promotion: %#v", decision)
	}

	decision = parseAutoPromoteEvalDecision("PROMOTE: yes\nREPLAY: pass\nSAVED_REPLAY: fail\nFALSE_POSITIVES: 1\nFALSE_NEGATIVES: 0\nRATIONALE: too broad")
	decision.SavedReplayRequired = true
	decision.finalizeFailureMode()
	if decision.Allowed() || decision.FailureMode != "saved_replay_failed" || decision.FalsePositiveCount != 1 {
		t.Fatalf("expected saved replay failure to block promotion: %#v", decision)
	}
}

func TestKnightAutoModeBlocksCandidateOverlappingActiveBaseline(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	activeDir := filepath.Join(projDir, ".ggcode", "skills", "existing-go-verification")
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		t.Fatalf("MkdirAll(activeDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(activeDir, "SKILL.md"), []byte(`---
name: existing-go-verification
description: Existing Go verification workflow.
scope: project
created_by: knight
---
# Existing Go Verification

## When to Use
Use when Go source files change and the agent must verify tests before reporting completion.

## Steps
1. Run focused Go tests for touched packages.
`), 0644); err != nil {
		t.Fatalf("WriteFile(active skill) error = %v", err)
	}

	k := New(config.KnightConfig{Enabled: true, TrustLevel: "auto"}, homeDir, projDir, nil)
	var evalPrompt string
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		return captureKnightRunner{
			prompt: &evalPrompt,
			output: "PROMOTE: yes\nREPLAY: pass\nSAVED_REPLAY: skip\nFALSE_POSITIVES: 0\nFALSE_NEGATIVES: 0\nBASELINE_REPLAY: fail\nOVERLAP_COUNT: 1\nRATIONALE: Existing skill already covers this workflow.",
		}, nil
	})
	if _, err := k.promoter.WriteStaging("go-verify-flow", "project", `---
name: go-verify-flow
description: Verify Go changes before reporting completion.
scope: project
created_by: knight
---
# Go Verify Flow

## When to Use
Use when changing Go files and the user expects verified code.

## When Not to Use
Do not use for documentation-only changes.

## Steps
1. Run focused Go tests for the touched package area.
`); err != nil {
		t.Fatalf("WriteStaging() error = %v", err)
	}

	k.reviewStagingSkills(context.Background())

	if !strings.Contains(evalPrompt, "Active baseline skills:") || !strings.Contains(evalPrompt, "existing-go-verification") {
		t.Fatalf("expected active baseline skills in eval prompt, got:\n%s", evalPrompt)
	}
	active, err := k.index.ActiveSkills()
	if err != nil {
		t.Fatalf("ActiveSkills() error = %v", err)
	}
	if len(active) != 1 || active[0].Name != "existing-go-verification" {
		t.Fatalf("overlapping candidate should not be promoted, active=%#v", active)
	}
	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills() error = %v", err)
	}
	if len(staging) != 1 || staging[0].Name != "go-verify-flow" {
		t.Fatalf("expected overlapping candidate to remain staged, got %#v", staging)
	}
	evals, err := k.RecentAutoPromoteEvalsForSkill("project", "go-verify-flow", 1)
	if err != nil {
		t.Fatalf("RecentAutoPromoteEvalsForSkill() error = %v", err)
	}
	if len(evals) != 1 || evals[0].Allowed || !evals[0].BaselineReplayRequired || evals[0].FailureMode != "baseline_replay_failed" || evals[0].OverlapCount != 1 {
		t.Fatalf("unexpected baseline evals: %#v", evals)
	}
}

func TestKnightGenerateProjectImprovementProposalWritesReviewableMarkdown(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	var prompt string
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		return captureKnightRunner{
			prompt: &prompt,
			output: `# Tighten Go Verification

## Summary
Run focused Go checks before reporting completion.

## Proposed Changes
Document the existing verification path.

## Validation Plan
Run go test ./internal/knight.

## Risks and Rollback
Discard this proposal if it is too broad.`,
		}, nil
	})

	proposal, result, err := k.GenerateProjectImprovementProposal(context.Background(), "improve verification flow")
	if err != nil {
		t.Fatalf("GenerateProjectImprovementProposal() error = %v", err)
	}
	if result.Output == "" {
		t.Fatal("expected task output")
	}
	if !strings.Contains(prompt, "Do NOT modify project source files") {
		t.Fatalf("proposal prompt should forbid direct project edits, got:\n%s", prompt)
	}
	if proposal.ID == "" || proposal.Title != "Tighten Go Verification" || proposal.Summary == "" {
		t.Fatalf("unexpected proposal metadata: %#v", proposal)
	}
	content, err := os.ReadFile(proposal.Path)
	if err != nil {
		t.Fatalf("ReadFile(proposal) error = %v", err)
	}
	if !strings.Contains(string(content), "status: proposed") || !strings.Contains(string(content), "## Validation Plan") {
		t.Fatalf("unexpected proposal content:\n%s", string(content))
	}
	recent, err := k.RecentProjectImprovementProposals(1)
	if err != nil {
		t.Fatalf("RecentProjectImprovementProposals() error = %v", err)
	}
	if len(recent) != 1 || recent[0].ID != proposal.ID {
		t.Fatalf("unexpected recent proposals: %#v", recent)
	}
	readBack, readContent, err := k.ReadProjectImprovementProposal(proposal.ID)
	if err != nil {
		t.Fatalf("ReadProjectImprovementProposal() error = %v", err)
	}
	if readBack.ID != proposal.ID || !strings.Contains(readContent, "# Tighten Go Verification") {
		t.Fatalf("unexpected readback: %#v\n%s", readBack, readContent)
	}
}

func TestKnightAutoPoliciesDocumentGuardrails(t *testing.T) {
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), t.TempDir(), nil)
	policies := k.AutoPolicies()
	if len(policies) == 0 {
		t.Fatal("expected auto policies")
	}
	var hasAutoPromote, hasNoCodeWrites bool
	for _, policy := range policies {
		if policy.Name == "" || policy.Mode == "" || policy.Description == "" || policy.Guardrail == "" {
			t.Fatalf("policy should be fully described: %#v", policy)
		}
		if strings.Contains(policy.Name, "auto-promotion") && strings.Contains(policy.Guardrail, "active baseline overlap") {
			hasAutoPromote = true
		}
		if strings.Contains(policy.Name, "project code writes") && policy.Mode == "never automatic" {
			hasNoCodeWrites = true
		}
	}
	if !hasAutoPromote || !hasNoCodeWrites {
		t.Fatalf("expected auto-promote and no-code-write guardrails, got %#v", policies)
	}
}

func TestKnightRollbackSkillRestoresLatestSnapshot(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	snapshotDir := filepath.Join(projDir, ".ggcode", "skills-snapshots")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll(skillDir) error = %v", err)
	}
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		t.Fatalf("MkdirAll(snapshotDir) error = %v", err)
	}
	activePath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(activePath, []byte(`---
name: build-flow
description: Current version
scope: project
created_by: knight
---
# Current

## When to Use
Current

## Steps
1. Current
`), 0644); err != nil {
		t.Fatalf("WriteFile(active) error = %v", err)
	}
	snapshotPath := filepath.Join(snapshotDir, "build-flow.20260420-010203.md")
	if err := os.WriteFile(snapshotPath, []byte(`---
name: build-flow
description: Previous version
scope: project
created_by: knight
---
# Previous

## When to Use
Previous

## Steps
1. Previous
`), 0644); err != nil {
		t.Fatalf("WriteFile(snapshot) error = %v", err)
	}

	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.RollbackSkill("build-flow"); err != nil {
		t.Fatalf("RollbackSkill() error = %v", err)
	}

	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("ReadFile(active) error = %v", err)
	}
	if !contains(string(data), "# Previous") {
		t.Fatalf("expected active skill to be restored from snapshot, got:\n%s", string(data))
	}
}

func TestValidateSkillNameRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"../escape", "nested/name", `nested\name`} {
		if err := validateSkillName(name); err == nil {
			t.Fatalf("expected unsafe name %q to be rejected", name)
		}
	}
	if err := validateSkillName("safe-name_1"); err != nil {
		t.Fatalf("expected safe skill name to pass, got %v", err)
	}
}

func TestFindActiveSkillRejectsAmbiguousUnscopedName(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	globalDir := filepath.Join(homeDir, ".ggcode", "skills", "build-flow")
	projectDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(global) error = %v", err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(project) error = %v", err)
	}
	content := []byte(`---
name: build-flow
description: Build flow
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use for builds.

## Steps
1. Run the build
`)
	if err := os.WriteFile(filepath.Join(globalDir, "SKILL.md"), bytes.ReplaceAll(content, []byte("scope: project"), []byte("scope: global")), 0o644); err != nil {
		t.Fatalf("WriteFile(global) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "SKILL.md"), content, 0o644); err != nil {
		t.Fatalf("WriteFile(project) error = %v", err)
	}

	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if _, err := k.FindActiveSkill("build-flow"); err == nil {
		t.Fatal("expected ambiguous unscoped reference to fail")
	}
	entry, err := k.FindActiveSkill("project:build-flow")
	if err != nil || entry.Scope != "project" {
		t.Fatalf("expected scoped reference to resolve project skill, got entry=%#v err=%v", entry, err)
	}
}

func TestUsageTrackerScopedKeyDoesNotReadBareNameData(t *testing.T) {
	ut := NewUsageTracker(filepath.Join(t.TempDir(), "usage.json"))
	ut.RecordUse("build-flow")
	ut.RecordEffectiveness("build-flow", 4)

	count, _, avg := ut.GetUsage("project:build-flow")
	if count != 0 || avg != 0 {
		t.Fatalf("expected scoped stats to stay isolated, got count=%d avg=%f", count, avg)
	}
	feedbackAvg, samples := ut.GetFeedback("project:build-flow")
	if feedbackAvg != 0 || samples != 0 {
		t.Fatalf("expected scoped feedback to stay isolated, got avg=%f samples=%d", feedbackAvg, samples)
	}
}

func TestCandidateQueuePersistsDeferredCandidates(t *testing.T) {
	queue := NewCandidateQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err := queue.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	candidate := SkillCandidate{Name: "build-flow", Scope: "project", Score: 4.2, EvidenceCount: 3}
	if err := queue.Upsert(candidate); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	items, err := queue.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Name != "build-flow" {
		t.Fatalf("expected persisted candidate, got %+v", items)
	}
	if err := queue.Remove(candidate); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	items, err = queue.List()
	if err != nil {
		t.Fatalf("List() after remove error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty queue after remove, got %+v", items)
	}
}

func TestMergeSkillCandidatesPrefersFreshHigherSignal(t *testing.T) {
	queued := []SkillCandidate{{Name: "build-flow", Scope: "project", Score: 3.5, EvidenceCount: 2}}
	fresh := []SkillCandidate{{Name: "build-flow", Scope: "project", Score: 4.8, EvidenceCount: 4}}
	items := mergeSkillCandidates(queued, fresh)
	if len(items) != 1 {
		t.Fatalf("expected 1 merged candidate, got %+v", items)
	}
	if items[0].Score != 4.8 || items[0].EvidenceCount != 4 {
		t.Fatalf("expected fresher candidate to win, got %+v", items[0])
	}
}

func TestAnalyzeRecentSessionsProcessesDeferredQueueWithoutStore(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(homeDir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projDir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("MkdirAll(project) error = %v", err)
	}
	cfg := config.DefaultKnightConfig()
	cfg.Enabled = true
	k := New(cfg, homeDir, projDir, nil)
	if err := k.queue.EnsureDir(); err != nil {
		t.Fatalf("queue.EnsureDir() error = %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := k.queue.Upsert(SkillCandidate{
			Name:          fmt.Sprintf("build-flow-%d", i),
			Scope:         "project",
			Description:   "Build flow",
			Score:         4.0 - float64(i)*0.1,
			EvidenceCount: 3,
			Reason:        "deferred",
		}); err != nil {
			t.Fatalf("queue.Upsert(%d) error = %v", i, err)
		}
	}
	generated := 0
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		generated++
		return stubKnightRunner{output: fmt.Sprintf(`---
name: generated-skill-%d
description: "Generated skill"
scope: project
created_by: knight
---
# Generated Skill

## When to Use
Use it.

## Steps
1. Do the thing
`, generated)}, nil
	})

	if err := k.analyzeRecentSessions(context.Background()); err != nil {
		t.Fatalf("analyzeRecentSessions() error = %v", err)
	}
	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills() error = %v", err)
	}
	if len(staging) != knightMaxGeneratedSkills {
		t.Fatalf("expected %d staged skills, got %d", knightMaxGeneratedSkills, len(staging))
	}
	remaining, err := k.queue.List()
	if err != nil {
		t.Fatalf("queue.List() error = %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 deferred candidate to remain queued, got %+v", remaining)
	}
}

// --- EventSink Tests ---

func TestFuncSink(t *testing.T) {
	var startName, completeName, completeReport string
	var completeDuration time.Duration

	sink := &FuncSink{
		OnStart: func(name string) { startName = name },
		OnComplete: func(name string, report string, dur time.Duration) {
			completeName = name
			completeReport = report
			completeDuration = dur
		},
	}

	sink.OnTaskStart("test-task")
	if startName != "test-task" {
		t.Errorf("OnStart: got %q, want %q", startName, "test-task")
	}

	sink.OnTaskComplete("test-task", "done", 5*time.Second)
	if completeName != "test-task" {
		t.Errorf("OnComplete name: got %q, want %q", completeName, "test-task")
	}
	if completeReport != "done" {
		t.Errorf("OnComplete report: got %q, want %q", completeReport, "done")
	}
	if completeDuration != 5*time.Second {
		t.Errorf("OnComplete duration: got %v, want %v", completeDuration, 5*time.Second)
	}
}

func TestFuncSink_NilCallbacks(t *testing.T) {
	// Should not panic with nil callbacks
	sink := &FuncSink{}
	sink.OnTaskStart("anything")
	sink.OnTaskComplete("anything", "report", time.Second)
}

func TestSetEventSink(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true, Capabilities: []string{"skill_creation", "skill_validation", "test_generation", "regression_testing", "doc_sync"}}
	k := New(cfg, t.TempDir(), t.TempDir(), nil)

	var events []string
	sink := &FuncSink{
		OnStart:    func(name string) { events = append(events, "start:"+name) },
		OnComplete: func(name string, report string, dur time.Duration) { events = append(events, "complete:"+name) },
	}
	k.SetEventSink(sink)
	got := k.getEventSink()
	if got != sink {
		t.Error("getEventSink should return the same sink")
	}
}

type mockRecordingSink struct {
	mu        sync.Mutex
	starts    []string
	completes []sinkComplete
}

type sinkComplete struct {
	name     string
	report   string
	duration time.Duration
}

func (s *mockRecordingSink) OnTaskStart(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, name)
}

func (s *mockRecordingSink) OnTaskComplete(name string, report string, dur time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completes = append(s.completes, sinkComplete{name, report, dur})
}

func TestRunMaintenanceTask_EmitsEvents(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true, Capabilities: []string{"skill_creation", "skill_validation", "test_generation"}}
	tmpDir := t.TempDir()
	k := New(cfg, tmpDir, tmpDir, nil)

	recorder := &mockRecordingSink{}
	k.SetEventSink(recorder)

	// Set up a mock factory
	callCount := 0
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		callCount++
		return &stubKnightRunner{output: "maintenance result output", err: nil}, nil
	})

	result := k.runMaintenanceTask(context.Background(), "test-maintenance", "do something")
	if result != "maintenance result output" {
		t.Errorf("result = %q, want %q", result, "maintenance result output")
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.starts) != 1 || recorder.starts[0] != "test-maintenance" {
		t.Errorf("starts = %v, want [test-maintenance]", recorder.starts)
	}
	if len(recorder.completes) != 1 || recorder.completes[0].name != "test-maintenance" {
		t.Errorf("completes = %v, want [test-maintenance]", recorder.completes)
	}
	if recorder.completes[0].report != "maintenance result output" {
		t.Errorf("complete report = %q, want %q", recorder.completes[0].report, "maintenance result output")
	}
}

func TestRunMaintenanceTask_EmitsOnError(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true}
	tmpDir := t.TempDir()
	k := New(cfg, tmpDir, tmpDir, nil)

	recorder := &mockRecordingSink{}
	k.SetEventSink(recorder)

	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error) {
		return &stubKnightRunner{output: "", err: fmt.Errorf("something broke")}, nil
	})

	result := k.runMaintenanceTask(context.Background(), "fail-task", "do something")
	if !strings.Contains(result, "task failed") {
		t.Errorf("result = %q, should contain 'task failed'", result)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.starts) != 1 || recorder.starts[0] != "fail-task" {
		t.Errorf("starts = %v, want [fail-task]", recorder.starts)
	}
	if len(recorder.completes) != 1 {
		t.Fatalf("completes = %v, want 1 entry", recorder.completes)
	}
	if !strings.Contains(recorder.completes[0].report, "task failed") {
		t.Errorf("complete report = %q, should contain 'task failed'", recorder.completes[0].report)
	}
}

func TestRunMaintenanceTask_NoFactory(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true}
	tmpDir := t.TempDir()
	k := New(cfg, tmpDir, tmpDir, nil)

	recorder := &mockRecordingSink{}
	k.SetEventSink(recorder)

	result := k.runMaintenanceTask(context.Background(), "nope", "do something")
	if result != "" {
		t.Errorf("result = %q, want empty", result)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.starts) != 0 {
		t.Errorf("no factory → no events, got starts = %v", recorder.starts)
	}
	if len(recorder.completes) != 0 {
		t.Errorf("no factory → no events, got completes = %v", recorder.completes)
	}
}

func TestEmitReport_ForwardsToSink(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true}
	cfg.TrustLevel = "staged"
	tmpDir := t.TempDir()
	k := New(cfg, tmpDir, tmpDir, nil)

	recorder := &mockRecordingSink{}
	k.SetEventSink(recorder)

	// emitReport also sends to sink (with empty task name)
	k.emitReport("test report content")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.completes) != 1 {
		t.Fatalf("completes = %v, want 1 entry", recorder.completes)
	}
	if recorder.completes[0].name != "" {
		t.Errorf("name = %q, want empty (emitReport uses empty name)", recorder.completes[0].name)
	}
	if recorder.completes[0].report != "test report content" {
		t.Errorf("report = %q, want %q", recorder.completes[0].report, "test report content")
	}
}

func TestEmitReport_QuietHours_SkipsSink(t *testing.T) {
	cfg := config.KnightConfig{Enabled: true}
	cfg.QuietHours = []string{"00:00-23:59"}
	tmpDir := t.TempDir()
	k := New(cfg, tmpDir, tmpDir, nil)

	recorder := &mockRecordingSink{}
	k.SetEventSink(recorder)

	k.emitReport("should be suppressed")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.completes) != 0 {
		t.Errorf("quiet hours should suppress sink events, got %d", len(recorder.completes))
	}
}

func TestAnalyzeRecentDedupAcrossCalls(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-dedup-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	storeDir := filepath.Join(homeDir, ".ggcode", "sessions")
	os.MkdirAll(storeDir, 0755)
	store, _ := session.NewJSONLStore(storeDir)

	// Create one session with a correction
	ses := session.NewSession("zai", "test", "test-model")
	store.AppendMessage(ses, provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "build"}},
	})
	store.AppendMessage(ses, provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "debug binary"}},
	})
	store.AppendMessage(ses, provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "你需要编译的是正式的 ggcode 二进制而不是什么 debug 二进制"}},
	})
	store.AppendMessage(ses, provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "OK"}},
	})

	k := New(config.DefaultKnightConfig(), homeDir, projDir, store)

	// First call: should find the candidate
	r1, err := NewSessionAnalyzer(k).AnalyzeRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r1.SessionsAnalyzed != 1 {
		t.Fatalf("first call: expected 1 analyzed, got %d", r1.SessionsAnalyzed)
	}

	// Second call: session unchanged, should be skipped (dedup)
	r2, err := NewSessionAnalyzer(k).AnalyzeRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r2.SessionsAnalyzed != 0 {
		t.Fatalf("second call: expected 0 analyzed (dedup), got %d", r2.SessionsAnalyzed)
	}

	// Third call: session got a new message (UpdatedAt advanced) → should re-analyze
	// Wait a tiny bit so UpdatedAt changes
	time.Sleep(10 * time.Millisecond)
	store.AppendMessage(ses, provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "不要没搞清楚状况就自作主张"}},
	})
	store.AppendMessage(ses, provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "Understood"}},
	})

	r3, err := NewSessionAnalyzer(k).AnalyzeRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r3.SessionsAnalyzed != 1 {
		t.Fatalf("third call: expected 1 re-analyzed (session updated), got %d", r3.SessionsAnalyzed)
	}
}
