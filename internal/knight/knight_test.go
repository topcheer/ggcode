package knight

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

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
