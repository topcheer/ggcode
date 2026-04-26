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

// TestEndToEnd_KnightLifecycle tests the complete Knight workflow:
// 1. Create Knight with real directories
// 2. Analyze a session with repeated build-test pattern
// 3. Discover skill candidate
// 4. Write to staging
// 5. Validate
// 6. Promote to active
// 7. Verify skill is loadable
func TestEndToEnd_KnightLifecycle(t *testing.T) {
	// Setup real directory structure
	dir, err := os.MkdirTemp("", "knight-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	os.MkdirAll(filepath.Join(homeDir, ".ggcode"), 0755)
	os.MkdirAll(filepath.Join(projDir, ".ggcode"), 0755)

	// Create a real session store with a session that has build-test pattern
	storeDir := filepath.Join(homeDir, ".ggcode", "sessions")
	os.MkdirAll(storeDir, 0755)
	store, err := session.NewJSONLStore(storeDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	// Create a session with repeated build-test pattern
	ses := session.NewSession("zai", "test", "test-model")
	ses.Title = "Build and test iteration"

	// Simulate 3 rounds of build-test (assistant calls tools, user returns results)
	for i := 0; i < 3; i++ {
		store.AppendMessage(ses, provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "build and test"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "tool_use", ToolName: "run_command", ToolID: fmt.Sprintf("build-%d", i)},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "tool_result", ToolID: fmt.Sprintf("build-%d", i), Output: "build success"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "tool_use", ToolName: "run_command", ToolID: fmt.Sprintf("test-%d", i)},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "tool_result", ToolID: fmt.Sprintf("test-%d", i), Output: "tests passed"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Build and tests passed"},
			},
		})
	}

	// Create Knight
	cfg := config.DefaultKnightConfig()
	cfg.Enabled = true
	cfg.TrustLevel = "staged"
	k := New(cfg, homeDir, projDir, store)

	// Step 1: Start Knight (ensures directories)
	if err := k.Start(context.Background()); err != nil {
		t.Fatalf("Knight Start: %v", err)
	}
	defer k.Stop()

	// Step 1: Verify the session has the right structure
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

	// Step 2: Analyze sessions
	analyzer := NewSessionAnalyzer(k)
	result, err := analyzer.AnalyzeRecent(context.Background())
	if err != nil {
		t.Fatalf("AnalyzeRecent: %v", err)
	}

	t.Logf("Analyzed %d sessions, found %d candidates", result.SessionsAnalyzed, len(result.SkillCandidates))

	if result.SessionsAnalyzed == 0 {
		t.Fatal("expected at least 1 session analyzed")
	}

	// Step 3: Check that we found patterns
	if len(result.SkillCandidates) == 0 {
		// Debug: manually run the heuristics
		toolCounts := make(map[string]int)
		var toolSequence []string
		for _, msg := range ses.Messages {
			for _, block := range msg.Content {
				if block.Type == "tool_use" {
					toolCounts[block.ToolName]++
					toolSequence = append(toolSequence, block.ToolName)
				}
			}
		}
		t.Logf("DEBUG tool counts: %v", toolCounts)
		t.Logf("DEBUG tool sequence: %v", toolSequence)
		patterns := detectRepeatedPatterns(toolSequence)
		t.Logf("DEBUG patterns: %v", patterns)
		t.Fatal("expected at least 1 skill candidate from build-test pattern")
	}

	// Step 4: Write the best candidate to staging
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

	// Step 5: Validate the staging skill
	staging, err := k.index.StagingSkills()
	if err != nil {
		t.Fatalf("StagingSkills: %v", err)
	}
	if len(staging) == 0 {
		// Debug: check if file exists and parse frontmatter
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

	// Step 6: Promote to active
	if err := k.PromoteStaging(best.Name); err != nil {
		t.Fatalf("PromoteStaging: %v", err)
	}
	t.Log("Promoted to active")

	// Step 7: Verify active skill is loadable
	active := k.index.FindActiveByName(best.Name)
	if active == nil {
		t.Fatalf("active skill '%s' not found after promotion", best.Name)
	}
	t.Logf("Active skill verified: name=%s scope=%s description=%s", active.Name, active.Scope, active.Meta.Description)

	// Step 8: Verify staging is reduced (may have leftover candidates)
	stagingAfter, _ := k.index.StagingSkills()
	t.Logf("Remaining staging skills after promotion: %d", len(stagingAfter))

	// Step 9: Verify budget tracking worked
	if !k.budget.CanSpend() {
		t.Fatal("expected budget available after e2e test")
	}
	t.Logf("Budget: used=%d remaining=%d", k.budget.Used(), k.budget.Remaining())
}
