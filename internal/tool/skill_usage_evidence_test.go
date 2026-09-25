package tool

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
)

// stubReloaderLister extends stubReloader with skill inventory listing.
type stubReloaderLister struct {
	stubReloader
	names []string
}

func (s *stubReloaderLister) SkillNames() []string { return s.names }

func TestSkillUsageEvidenceLabel(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-2 * 24 * time.Hour)
	stale := now.Add(-45 * 24 * time.Hour)

	tests := []struct {
		name string
		ev   commands.SkillUsageEvidence
		ok   bool
		want string
	}{
		{"missing entry", commands.SkillUsageEvidence{}, false, "(never used)"},
		{"zero count", commands.SkillUsageEvidence{UsageCount: 0, LastUsedAt: fresh.UnixMilli()}, true, "(never used)"},
		{"missing timestamp", commands.SkillUsageEvidence{UsageCount: 3}, true, "(never used)"},
		{"fresh used", commands.SkillUsageEvidence{UsageCount: 3, LastUsedAt: fresh.UnixMilli()}, true, "(used 3x, last 2d ago)"},
		{"stale used", commands.SkillUsageEvidence{UsageCount: 1, LastUsedAt: stale.UnixMilli()}, true, "(used 1x, last 45d ago, stale)"},
		{"hours old", commands.SkillUsageEvidence{UsageCount: 2, LastUsedAt: now.Add(-5 * time.Hour).UnixMilli()}, true, "(used 2x, last 5h ago)"},
	}
	for _, tc := range tests {
		if got := skillUsageEvidenceLabel(tc.ev, tc.ok, now); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSearchSkillsSurfacesUsageEvidence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Record usage only for the proven skill.
	if err := commands.RecordUsage("r76-zeta"); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	lookup := fakeSkillLookup{skills: map[string]*commands.Command{
		"r76-zeta":  {Name: "r76-zeta", Description: "proven workflow"},
		"r76-alpha": {Name: "r76-alpha", Description: "unproven workflow"},
	}}
	st := SkillTool{Skills: lookup, NameLister: stubNameLister([]string{"r76-zeta", "r76-alpha"})}

	res := st.searchSkills("r76")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "r76-zeta") || !strings.Contains(res.Content, "r76-alpha") {
		t.Fatalf("expected both skills listed, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "(never used)") {
		t.Errorf("expected (never used) evidence marker, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "used 1x") {
		t.Errorf("expected usage count evidence, got: %s", res.Content)
	}
	// Tie-break: proven skill ranks above never-used one despite name order.
	zetaIdx := strings.Index(res.Content, "r76-zeta")
	alphaIdx := strings.Index(res.Content, "r76-alpha")
	if zetaIdx > alphaIdx {
		t.Errorf("expected proven skill ranked first; zeta@%d alpha@%d\n%s", zetaIdx, alphaIdx, res.Content)
	}
}

func TestBuildSkillInventoryNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := buildSkillInventoryNote(nil); got != "" {
		t.Errorf("empty inventory should produce no note, got %q", got)
	}

	got := buildSkillInventoryNote([]string{"legacy-a", "legacy-b"})
	wantPrefix := "Skill inventory: 2 total, 0 with recorded usage, 2 never used."
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("got %q, want prefix %q", got, wantPrefix)
	}
	if !strings.Contains(got, "Prefer reusing or refining a proven skill") {
		t.Errorf("expected reuse advisory in %q", got)
	}

	if err := commands.RecordUsage("legacy-a"); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	got = buildSkillInventoryNote([]string{"legacy-a", "legacy-b"})
	wantPrefix = "Skill inventory: 2 total, 1 with recorded usage, 1 never used."
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("after one use: got %q, want prefix %q", got, wantPrefix)
	}

	// Fully proven inventory omits the advisory.
	if err := commands.RecordUsage("legacy-b"); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	got = buildSkillInventoryNote([]string{"legacy-a", "legacy-b"})
	if strings.Contains(got, "Prefer reusing") {
		t.Errorf("fully proven inventory should omit advisory, got %q", got)
	}
}

func TestCreateSkillAppendsInventoryNote(t *testing.T) {
	dir := t.TempDir()
	tool := CreateSkillTool{
		CommandMgr: &stubReloaderLister{
			stubReloader: stubReloader{cmds: map[string]*commands.Command{}},
			names:        []string{"existing-unused"},
		},
		WorkingDir: dir,
	}

	input, _ := json.Marshal(map[string]string{
		"name":        "new-workflow",
		"description": "A fresh workflow",
		"content":     "steps",
		"scope":       "project",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Skill inventory: 1 total, 0 with recorded usage, 1 never used") {
		t.Errorf("expected inventory note in result, got: %s", result.Content)
	}
}

func TestCreateSkillWithoutListerOmitsNote(t *testing.T) {
	dir := t.TempDir()
	tool := CreateSkillTool{
		CommandMgr: &stubReloader{cmds: map[string]*commands.Command{}},
		WorkingDir: dir,
	}
	input, _ := json.Marshal(map[string]string{
		"name":        "plain-workflow",
		"description": "No lister",
		"content":     "steps",
		"scope":       "project",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "Skill inventory") {
		t.Errorf("non-listing manager should omit inventory note, got: %s", result.Content)
	}
}
