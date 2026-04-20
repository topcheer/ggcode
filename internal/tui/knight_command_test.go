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
