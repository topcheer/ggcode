package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/knight"
)

// sa-162: dedicated tests for the decomposed /knight dispatcher paths that
// the original monolith's test suite did not cover: on/off config
// persistence, the nil-knight gate, unknown-subcommand usage, the #1363
// global-scope promotion gate, staging rejection, and log clearing.

func knightTestWriteStagingSkill(t *testing.T, baseDir, scope, name string) {
	t.Helper()
	stagingDir := filepath.Join(baseDir, ".ggcode", "skills-staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	stagingPath := filepath.Join(stagingDir, "knight-20260420-"+name+".md")
	content := `---
name: ` + name + `
description: Build the project reliably
scope: ` + scope + `
created_by: knight
---
# ` + name + `

## When to Use
Use this when validating builds.

## Steps
1. Run the build

## When Not to Use
Do not use this for unrelated tasks.
`
	if err := os.WriteFile(stagingPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func knightTestStartKnight(t *testing.T) (*knight.Knight, string, string) {
	t.Helper()
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(k.Stop)
	return k, homeDir, projDir
}

func TestKnightOnOffWithoutConfig(t *testing.T) {
	m := newTestModel() // config == nil, knight == nil
	for _, subcmd := range []string{"on", "off"} {
		if cmd := m.handleKnightCommand([]string{"/knight", subcmd}); cmd != nil {
			t.Fatalf("expected %q to complete synchronously", subcmd)
		}
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Config not available") {
		t.Fatalf("expected config-not-available message, got %q", out)
	}
}

func TestKnightOnOffPersistsConfigAndTogglesRuntime(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(dir, "config.yaml")

	k := knight.New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer k.Stop()

	m := newTestModel()
	m.SetConfig(cfg)
	m.SetKnight(k)

	if cmd := m.handleKnightCommand([]string{"/knight", "on"}); cmd != nil {
		t.Fatal("expected on to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Knight enabled and started.") {
		t.Fatalf("expected enabled-and-started message, got %q", out)
	}
	if cmd := m.handleKnightCommand([]string{"/knight", "off"}); cmd != nil {
		t.Fatal("expected off to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Knight disabled and stopped.") {
		t.Fatalf("expected disabled-and-stopped message, got %q", out)
	}
}

func TestKnightOnOffWithoutRuntimeShowsRestartNotice(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(dir, "config.yaml")

	m := newTestModel()
	m.SetConfig(cfg)

	if cmd := m.handleKnightCommand([]string{"/knight", "on"}); cmd != nil {
		t.Fatal("expected on to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Knight enabled. Restart to apply.") {
		t.Fatalf("expected restart notice for on, got %q", out)
	}
	if cmd := m.handleKnightCommand([]string{"/knight", "off"}); cmd != nil {
		t.Fatal("expected off to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Knight disabled. Restart to apply.") {
		t.Fatalf("expected restart notice for off, got %q", out)
	}
}

func TestKnightSubcommandRequiresRunningKnight(t *testing.T) {
	m := newTestModel() // knight == nil
	if cmd := m.handleKnightCommand([]string{"/knight", "budget"}); cmd != nil {
		t.Fatal("expected budget gate to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Knight is not available (only in daemon mode)") {
		t.Fatalf("expected nil-knight gate message, got %q", out)
	}
}

func TestKnightUnknownSubcommandShowsUsage(t *testing.T) {
	k, _, _ := knightTestStartKnight(t)
	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "bogus"}); cmd != nil {
		t.Fatal("expected usage to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Knight commands: status, budget") {
		t.Fatalf("expected usage listing, got %q", out)
	}
}

func TestKnightApproveGlobalScopeRequiresConfirm(t *testing.T) {
	k, homeDir, _ := knightTestStartKnight(t)
	knightTestWriteStagingSkill(t, homeDir, "global", "gskill")

	m := newTestModel()
	m.SetKnight(k)

	// Without --confirm-global the #1363 gate must block promotion.
	if cmd := m.handleKnightCommand([]string{"/knight", "approve", "gskill"}); cmd != nil {
		t.Fatal("expected approve gate to complete synchronously")
	}
	out := renderedOutput(&m)
	if !strings.Contains(out, "is GLOBAL scope") || !strings.Contains(out, "Not promoted") {
		t.Fatalf("expected global-scope confirmation gate, got %q", out)
	}
	if staging, _ := k.Index().StagingSkills(); len(staging) != 1 {
		t.Fatalf("expected staging skill to remain, got %d entries", len(staging))
	}

	// With --confirm-global the promotion proceeds.
	if cmd := m.handleKnightCommand([]string{"/knight", "approve", "gskill", "--confirm-global"}); cmd != nil {
		t.Fatal("expected confirmed approve to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Skill 'gskill' promoted") {
		t.Fatalf("expected promotion confirmation, got %q", out)
	}
	if staging, _ := k.Index().StagingSkills(); len(staging) != 0 {
		t.Fatalf("expected staging skill to be consumed, got %d entries", len(staging))
	}
}

func TestKnightRejectCommandRejectsStagingSkill(t *testing.T) {
	k, _, projDir := knightTestStartKnight(t)
	knightTestWriteStagingSkill(t, projDir, "project", "build-flow")

	m := newTestModel()
	m.SetKnight(k)
	if cmd := m.handleKnightCommand([]string{"/knight", "reject", "build-flow"}); cmd != nil {
		t.Fatal("expected reject to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Skill 'build-flow' rejected") {
		t.Fatalf("expected rejection confirmation, got %q", out)
	}
	if staging, _ := k.Index().StagingSkills(); len(staging) != 0 {
		t.Fatalf("expected staging skill to be removed, got %d entries", len(staging))
	}
}

func TestKnightScenariosAndRejectsClear(t *testing.T) {
	k, _, _ := knightTestStartKnight(t)
	m := newTestModel()
	m.SetKnight(k)

	if cmd := m.handleKnightCommand([]string{"/knight", "scenarios", "clear"}); cmd != nil {
		t.Fatal("expected scenarios clear to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Cleared saved replay scenarios") {
		t.Fatalf("expected scenarios-clear message, got %q", out)
	}
	if cmd := m.handleKnightCommand([]string{"/knight", "rejects", "clear"}); cmd != nil {
		t.Fatal("expected rejects clear to complete synchronously")
	}
	if out := renderedOutput(&m); !strings.Contains(out, "Cleared reject feedback log") {
		t.Fatalf("expected rejects-clear message, got %q", out)
	}
}
