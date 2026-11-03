//go:build integration_local

package knight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestEndToEnd_KnightLifecycle(t *testing.T) {

	dir, err := os.MkdirTemp("", "knight-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	os.MkdirAll(filepath.Join(homeDir, ".ggcode"), 0755)
	os.MkdirAll(filepath.Join(projDir, ".ggcode"), 0755)

	storeDir := filepath.Join(homeDir, ".ggcode", "sessions")
	os.MkdirAll(storeDir, 0755)
	store, err := session.NewJSONLStore(storeDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	ses := session.NewSession("zai", "test", "test-model")
	ses.Title = "Build and test iteration"

	store.AppendMessage(ses, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "find the debug log path"},
		},
	})
	store.AppendMessage(ses, provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "The debug logs should be in /var/log/ggcode/"},
			{Type: "tool_use", ToolName: "run_command", ToolID: "find-log"},
		},
	})
	store.AppendMessage(ses, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "find-log", Output: "no such directory", IsError: true},
		},
	})

	store.AppendMessage(ses, provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "tool_use", ToolName: "run_command", ToolID: "find-log2"},
		},
	})
	store.AppendMessage(ses, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "find-log2", Output: "/tmp/ggcode-debug/"},
		},
	})
	store.AppendMessage(ses, provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "Found it at /tmp/ggcode-debug/"},
		},
	})

	store.AppendMessage(ses, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "为什么你不会去看看逻辑再得出你的结论呢，debug log path is defined in internal/debug/debug.go, always read the code first"},
		},
	})
	store.AppendMessage(ses, provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "You're right, I should have read internal/debug/debug.go first to find the defaultLogDir constant"},
		},
	})

	cfg := config.DefaultKnightConfig()
	cfg.Enabled = true
	cfg.TrustLevel = "staged"
	k := New(cfg, homeDir, projDir, store)

	if err := k.Start(context.Background()); err != nil {
		t.Fatalf("Knight Start: %v", err)
	}
	defer k.Stop()

	t.Logf("Session ID: %s, Messages: %d", ses.ID, len(ses.Messages))
	toolUseCount := 0
	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" {
				toolUseCount++
				t.Logf("  tool_use: %s", block.ToolName)
			}
		}
	}
	t.Logf("Total tool_use blocks: %d", toolUseCount)

	if toolUseCount == 0 {
		t.Fatal("session has no tool_use blocks — session store may not persist tool_use in AppendMessage")
	}

	analyzer := NewSessionAnalyzer(k)
	result, err := analyzer.AnalyzeRecent(context.Background())
	if err != nil {
		t.Fatalf("AnalyzeRecent: %v", err)
	}

	t.Logf("Analyzed %d sessions, found %d candidates", result.SessionsAnalyzed, len(result.SkillCandidates))

	if result.SessionsAnalyzed == 0 {
		t.Fatal("expected at least 1 session analyzed")
	}

	if len(result.SkillCandidates) == 0 {
		t.Fatal("expected at least 1 skill candidate (correction or failure-fix)")
	}

	best := result.SkillCandidates[0]
	t.Logf("Best candidate: %s (%s) score=%.1f reason=%s", best.Name, best.Scope, best.Score, best.Reason)

	skillContent := fmt.Sprintf(`---
name: %s
description: "%s"
scope: %s
created_by: knight
created_from: %s
---
# %s

## Steps
1. go build ./...
2. go test ./...

## Notes
- Use -count=1 to avoid caching
- Use -tags=!integration to skip integration tests`, best.Name, best.Description, best.Scope, ses.ID, best.Name)

	stagingPath, err := k.promoter.WriteStaging(best.Name, best.Scope, skillContent)
	if err != nil {
		t.Fatalf("WriteStaging: %v", err)
	}
	t.Logf("Written to staging: %s", stagingPath)

	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills: %v", err)
	}
	if len(staging) == 0 {

		stagingDir := filepath.Join(homeDir, ".ggcode", "skills-staging")
		entries, _ := os.ReadDir(stagingDir)
		for _, e := range entries {
			fp := filepath.Join(stagingDir, e.Name())
			data, _ := os.ReadFile(fp)
			t.Logf("File %s content:\n%s", e.Name(), string(data))
			meta, err := parseSkillFile(fp)
			t.Logf("Parse result: meta=%+v err=%v", meta, err)
		}
		t.Fatalf("no staging skills found. Files in %s: %v", stagingDir, entries)
	}

	validation := ValidateSkill(staging[0])
	t.Logf("Validation: valid=%v errors=%v warnings=%v", validation.Valid, validation.Errors, validation.Warnings)
	if !validation.Valid {
		t.Fatalf("skill validation failed: %v", validation.Errors)
	}

	if err := k.PromoteStaging(best.Name); err != nil {
		t.Fatalf("PromoteStaging: %v", err)
	}
	t.Log("Promoted to active")

	active := k.index.FindActiveByName(best.Name)
	if active == nil {
		t.Fatalf("active skill '%s' not found after promotion", best.Name)
	}
	t.Logf("Active skill verified: name=%s scope=%s description=%s", active.Name, active.Scope, active.Meta.Description)

	stagingAfter, _ := k.index.StagingSkills()
	t.Logf("Remaining staging skills after promotion: %d", len(stagingAfter))

	if !k.budget.CanSpend() {
		t.Fatal("expected budget available after e2e test")
	}
	t.Logf("Budget: used=%d remaining=%d", k.budget.Used(), k.budget.Remaining())
}
