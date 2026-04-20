package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/provider"
)

type testKnightRunner struct {
	output string
}

func (r testKnightRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: r.output})
	return nil
}

func TestKnightRateCommandRecordsFeedback(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: build-flow
description: Build the project reliably
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use this when validating builds.

## Steps
1. Run the build

## When Not to Use
Do not use this for unrelated tasks.
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "rate", "build-flow", "5"}); cmd != nil {
		t.Fatal("expected rate command to complete synchronously")
	}

	output := m.output.String()
	if !strings.Contains(output, "Rated skill 'build-flow' 5/5") {
		t.Fatalf("expected rating confirmation, got %q", output)
	}

	avg, samples := k.SkillFeedback("build-flow")
	if avg != 5.0 || samples != 1 {
		t.Fatalf("expected recorded feedback avg=5.0 samples=1, got avg=%f samples=%d", avg, samples)
	}
}

func TestKnightSkillsCommandShowsRuntimeFeedback(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: build-flow
description: Build the project reliably
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use this when validating builds.

## Steps
1. Run the build

## When Not to Use
Do not use this for unrelated tasks.
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()
	k.RecordSkillUse("build-flow")
	k.RecordSkillEffectiveness("build-flow", 4)

	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "skills"}); cmd != nil {
		t.Fatal("expected skills command to complete synchronously")
	}

	output := m.output.String()
	if !strings.Contains(output, "feedback: 4.0/5 (1)") {
		t.Fatalf("expected runtime feedback in skills output, got %q", output)
	}
	if !strings.Contains(output, "used: 1") {
		t.Fatalf("expected runtime usage in skills output, got %q", output)
	}
}

func TestKnightRunCommandExecutesTask(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	k.SetFactory(func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (knight.AgentRunner, error) {
		return testKnightRunner{output: "Added tests and verified they pass."}, nil
	})
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	m := newTestModel()
	m.SetKnight(k)
	cmd := m.handleKnightCommand([]string{"/knight", "run", "add", "tests"})
	if cmd == nil {
		t.Fatal("expected /knight run to return async command")
	}

	msg := cmd()
	updated, followup := m.Update(msg)
	if followup != nil {
		t.Fatal("expected no follow-up command after knight task result")
	}
	next := updated.(Model)
	output := next.output.String()
	if !strings.Contains(output, "Knight task completed: add tests") {
		t.Fatalf("expected task completion header, got %q", output)
	}
	if !strings.Contains(output, "Added tests and verified they pass.") {
		t.Fatalf("expected Knight task summary, got %q", output)
	}
}

func TestKnightFreezeAndUnfreezeCommands(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: build-flow
description: Build the project reliably
scope: project
created_by: knight
frozen: false
---
# Build Flow

## When to Use
Use this when validating builds.

## Steps
1. Run the build

## When Not to Use
Do not use this for unrelated tasks.
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "freeze", "build-flow"}); cmd != nil {
		t.Fatal("expected freeze command to complete synchronously")
	}
	if !strings.Contains(m.output.String(), "frozen") {
		t.Fatalf("expected freeze confirmation, got %q", m.output.String())
	}
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "frozen: true") {
		t.Fatal("expected frozen flag after /knight freeze")
	}

	if cmd := m.handleKnightCommand([]string{"/knight", "unfreeze", "build-flow"}); cmd != nil {
		t.Fatal("expected unfreeze command to complete synchronously")
	}
	data, err = os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "frozen: false") {
		t.Fatal("expected unfrozen flag after /knight unfreeze")
	}
}

func TestKnightRollbackCommand(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	skillDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	snapshotDir := filepath.Join(projDir, ".ggcode", "skills-snapshots")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skillDir) error = %v", err)
	}
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(snapshotDir) error = %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
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
`), 0o644); err != nil {
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
`), 0o644); err != nil {
		t.Fatalf("WriteFile(snapshot) error = %v", err)
	}

	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "rollback", "build-flow"}); cmd != nil {
		t.Fatal("expected rollback command to complete synchronously")
	}

	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(active) error = %v", err)
	}
	if !strings.Contains(string(data), "# Previous") {
		t.Fatalf("expected rollback to restore previous snapshot, got:\n%s", string(data))
	}
}

func TestKnightBudgetCommandShowsUsage(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "budget"}); cmd != nil {
		t.Fatal("expected budget command to complete synchronously")
	}
	if !strings.Contains(m.output.String(), "Knight budget:") {
		t.Fatalf("expected budget output, got %q", m.output.String())
	}
}

func TestKnightReviewCommandShowsStagingSummary(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	stagingDir := filepath.Join(projDir, ".ggcode", "skills-staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	stagingPath := filepath.Join(stagingDir, "knight-20260420-build-flow.md")
	if err := os.WriteFile(stagingPath, []byte(`---
name: build-flow
description: Build the project reliably
scope: project
created_by: knight
---
# Build Flow

## When to Use
Use this when validating builds.

## Steps
1. Run the build

## When Not to Use
Do not use this for unrelated tasks.
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "review"}); cmd != nil {
		t.Fatal("expected review command to complete synchronously")
	}
	if !strings.Contains(m.output.String(), "Staging skills (1):") || !strings.Contains(m.output.String(), "build-flow") {
		t.Fatalf("expected staging summary, got %q", m.output.String())
	}
}
