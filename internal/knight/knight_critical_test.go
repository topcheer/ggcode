package knight

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// --- Promote tests ---

func TestPromoterPromoteStagingToActive(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-promote-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")

	p := NewPromoter(homeDir, projDir)

	// Write a staging skill
	content := `---
name: build-flow
description: Build and test the project
scope: project
created_by: knight
created_at: "2026-04-19"
---
# Build Flow

## Steps
1. go build
2. go test`
	stagingPath, err := p.WriteStaging("build-flow", "project", content)
	if err != nil {
		t.Fatalf("WriteStaging: %v", err)
	}

	// Promote
	entry := &SkillEntry{
		Name:    "build-flow",
		Meta:    SkillMeta{Name: "build-flow", Description: "Build and test the project", Scope: "project", CreatedBy: "knight"},
		Path:    stagingPath,
		Scope:   "project",
		Staging: true,
	}
	if err := p.Promote(entry); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Verify active skill exists
	activePath := filepath.Join(projDir, ".ggcode", "skills", "build-flow", "SKILL.md")
	if _, err := os.Stat(activePath); os.IsNotExist(err) {
		t.Fatalf("active skill not created at %s", activePath)
	}

	// Verify staging file removed
	if _, err := os.Stat(stagingPath); !os.IsNotExist(err) {
		t.Fatal("staging file should be removed after promote")
	}

	// Verify content was preserved (with updated_at added)
	data, _ := os.ReadFile(activePath)
	body := string(data)
	if !contains(body, "Build Flow") {
		t.Errorf("skill content missing 'Build Flow', got:\n%s", body)
	}
	if !contains(body, "updated_at:") {
		t.Errorf("frontmatter missing updated_at, got:\n%s", body)
	}
}

func TestPromoterPromoteCreatesSnapshot(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-snap-*")
	defer os.RemoveAll(dir)

	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	p := NewPromoter(homeDir, projDir)

	// Create an existing active skill
	existingDir := filepath.Join(projDir, ".ggcode", "skills", "build-flow")
	os.MkdirAll(existingDir, 0755)
	existingContent := `---
name: build-flow
description: Old version
---
# Old`
	os.WriteFile(filepath.Join(existingDir, "SKILL.md"), []byte(existingContent), 0644)

	// Write staging with new version
	newContent := `---
name: build-flow
description: New version
scope: project
created_by: knight
---
# New`
	stagingPath, _ := p.WriteStaging("build-flow", "project", newContent)

	// Promote (should create snapshot of old version)
	entry := &SkillEntry{
		Name:    "build-flow",
		Meta:    SkillMeta{Name: "build-flow", Description: "New version"},
		Path:    stagingPath,
		Scope:   "project",
		Staging: true,
	}
	if err := p.Promote(entry); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Verify snapshot exists
	snapDir := filepath.Join(projDir, ".ggcode", "skills-snapshots")
	entries, err := os.ReadDir(snapDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected snapshot in %s, got err=%v entries=%d", snapDir, err, len(entries))
	}

	// Snapshot should contain old content
	snapPath := filepath.Join(snapDir, entries[0].Name())
	snapData, _ := os.ReadFile(snapPath)
	if !contains(string(snapData), "Old version") {
		t.Errorf("snapshot should contain old version, got:\n%s", string(snapData))
	}

	// Active should now have new version
	activeData, _ := os.ReadFile(filepath.Join(projDir, ".ggcode", "skills", "build-flow", "SKILL.md"))
	if !contains(string(activeData), "New version") {
		t.Errorf("active should have new version, got:\n%s", string(activeData))
	}
}

func TestPromoterRejectsNonStaging(t *testing.T) {
	dir, _ := os.MkdirTemp("", "knight-reject-*")
	defer os.RemoveAll(dir)
	p := NewPromoter(dir, dir)

	entry := &SkillEntry{Name: "test", Scope: "project", Staging: false}
	if err := p.Reject(entry); err == nil {
		t.Fatal("expected error when rejecting non-staging skill")
	}
}

// --- Analyzer tests ---

func TestAnalyzeSession_TooShort(t *testing.T) {
	sa := &SessionAnalyzer{}
	// Session with 3 messages — too short to be interesting
	ses := &session.Session{Messages: make([]provider.Message, 3)}
	candidates := sa.analyzeSession(ses)
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates for short session, got %d", len(candidates))
	}
}

func TestAnalyzeSession_BuildVerifyPattern(t *testing.T) {
	sa := &SessionAnalyzer{}
	// Simulate a session with build-verify pattern
	ses := &session.Session{ID: "test-123", Messages: make([]provider.Message, 20)}
	// Inject tool_use blocks simulating: run_command x4, read_file x3
	for i := 0; i < 4; i++ {
		ses.Messages[i] = provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "tool_use", ToolName: "run_command"},
			},
		}
	}
	for i := 4; i < 7; i++ {
		ses.Messages[i] = provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "tool_use", ToolName: "read_file"},
			},
		}
	}

	candidates := sa.analyzeSession(ses)
	// Should find build-verify pattern (run_command>=3 && read_file>=2)
	found := false
	for _, c := range candidates {
		if c.Name == "build-and-verify" {
			found = true
			if c.Scope != "project" {
				t.Errorf("expected scope=project, got %s", c.Scope)
			}
		}
	}
	if !found {
		t.Errorf("expected build-and-verify candidate, got %d candidates", len(candidates))
	}
}

// --- Scope inference tests ---

func TestInferScope(t *testing.T) {
	tests := []struct {
		tools []string
		want  string
	}{
		{[]string{"run_command"}, "project"},
		{[]string{"edit_file"}, "project"},
		{[]string{"read_file", "search_files"}, "project"},
		{[]string{"read_file", "edit_file", "run_command"}, "project"},
		{[]string{"web_fetch", "web_search"}, "global"},
		{[]string{"write_file"}, "project"},
	}
	for _, tt := range tests {
		got := inferScope(tt.tools)
		if got != tt.want {
			t.Errorf("inferScope(%v) = %q, want %q", tt.tools, got, tt.want)
		}
	}
}

func TestParseQuietWindow(t *testing.T) {
	start, end, ok := parseQuietWindow("23:00-06:30")
	if !ok {
		t.Fatal("expected quiet window to parse")
	}
	if start != 23*60 || end != 6*60+30 {
		t.Fatalf("unexpected quiet window parse result: start=%d end=%d", start, end)
	}
}

func TestKnightInQuietHours(t *testing.T) {
	k := &Knight{cfg: config.KnightConfig{QuietHours: []string{"23:00-06:30"}}}
	if !k.inQuietHours(time.Date(2026, 4, 20, 23, 15, 0, 0, time.Local)) {
		t.Fatal("expected 23:15 to be inside quiet hours")
	}
	if !k.inQuietHours(time.Date(2026, 4, 21, 5, 45, 0, 0, time.Local)) {
		t.Fatal("expected 05:45 to be inside wrapped quiet hours")
	}
	if k.inQuietHours(time.Date(2026, 4, 20, 12, 0, 0, 0, time.Local)) {
		t.Fatal("expected noon to be outside quiet hours")
	}
}

// --- Config tests ---

func TestKnightConfigDefaults(t *testing.T) {
	cfg := config.Config{}
	kc := cfg.Knight()

	if kc.DailyTokenBudget != 5_000_000 {
		t.Errorf("expected default budget 5M, got %d", kc.DailyTokenBudget)
	}
	if kc.TrustLevel != "staged" {
		t.Errorf("expected default trust_level 'staged', got %q", kc.TrustLevel)
	}
	if kc.IdleDelaySec != 300 {
		t.Errorf("expected default idle_delay 300, got %d", kc.IdleDelaySec)
	}
	if len(kc.Capabilities) == 0 {
		t.Error("expected default capabilities")
	}
}

func TestKnightConfigOverride(t *testing.T) {
	cfg := config.Config{
		KnightConfig: config.KnightConfig{
			Enabled:          true,
			TrustLevel:       "auto",
			DailyTokenBudget: 10_000_000,
		},
	}
	kc := cfg.Knight()

	if !kc.Enabled {
		t.Error("expected enabled")
	}
	if kc.TrustLevel != "auto" {
		t.Errorf("expected trust_level 'auto', got %q", kc.TrustLevel)
	}
	if kc.DailyTokenBudget != 10_000_000 {
		t.Errorf("expected budget 10M, got %d", kc.DailyTokenBudget)
	}
}

// helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
