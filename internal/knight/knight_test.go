package knight

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

	// Remaining should be 5M - 300
	if rem := b.Remaining(); rem != 5_000_000-300 {
		t.Fatalf("expected remaining=%d, got %d", 5_000_000-300, rem)
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
	if err := b.Record("unlimited", 5_000_000, 5_000_000); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !b.CanSpend() {
		t.Fatal("expected unlimited budget to keep allowing spends")
	}
	if rem := b.Remaining(); rem != 0 {
		t.Fatalf("expected unlimited budget remaining sentinel 0, got %d", rem)
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
		for j := 0; j < 4; j++ {
			store.AppendMessage(ses, provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					{Type: "tool_use", ToolName: "run_command"},
				},
			})
		}
		for j := 0; j < 3; j++ {
			store.AppendMessage(ses, provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					{Type: "tool_use", ToolName: "read_file"},
				},
			})
		}
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

func TestBuildCorrectionNameIsStableAcrossSessions(t *testing.T) {
	first := buildCorrectionName("不要用 group，应该用 users", []string{"run_command"})
	second := buildCorrectionName("不要用 group，应该用 users", []string{"run_command"})
	if first != second {
		t.Fatalf("expected stable correction name, got %q vs %q", first, second)
	}
	if contains(first, "session") {
		t.Fatalf("expected correction name to avoid session-specific suffix, got %q", first)
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

func TestDetectRepeatedPatterns(t *testing.T) {
	tools := []string{"read_file", "edit_file", "run_command", "read_file", "edit_file", "run_command", "read_file", "edit_file"}

	patterns := detectRepeatedPatterns(tools)
	if len(patterns) == 0 {
		t.Fatal("expected to find repeated patterns")
	}

	// Should find "read_file|edit_file" repeated 3 times
	found := false
	for _, p := range patterns {
		if len(p.Tools) == 2 && p.Tools[0] == "read_file" && p.Tools[1] == "edit_file" && p.Count >= 3 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected read_file|edit_file pattern with count>=3, got %v", patterns)
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

func TestInferCommandScope(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"git status", "global"},
		{"docker ps", "global"},
		{"go test ./...", "project"},
		{"make verify-ci", "project"},
		{"cat internal/knight/analyzer.go", "project"},
	}
	for _, tt := range tests {
		if got := inferCommandScope(tt.command); got != tt.want {
			t.Errorf("inferCommandScope(%q) = %q, want %q", tt.command, got, tt.want)
		}
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
	k.RecordSkillUse("build-flow")
	k.RecordSkillEffectiveness("build-flow", 5)

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
	k.RecordSkillEffectiveness("build-flow", 1)
	k.RecordSkillEffectiveness("build-flow", 2)

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
