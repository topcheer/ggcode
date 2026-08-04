package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

// fakeSkillLookup implements SkillLookup for testing.
type fakeSkillLookup struct {
	skills map[string]*commands.Command
}

func (f fakeSkillLookup) Get(name string) (*commands.Command, bool) {
	cmd, ok := f.skills[name]
	return cmd, ok
}

func (f fakeSkillLookup) SkillNames() []string {
	names := make([]string, 0, len(f.skills))
	for name := range f.skills {
		names = append(names, name)
	}
	return names
}

// fakeSkillLookup also satisfies skillUsageRecorder.
func (f fakeSkillLookup) RecordUsage(name string) {}

func makeFakeSkills() fakeSkillLookup {
	return fakeSkillLookup{
		skills: map[string]*commands.Command{
			"deploy-aws": {
				Name:        "deploy-aws",
				Description: "Deploy application to AWS ECS",
				WhenToUse:   "Use when deploying to AWS",
				Enabled:     true,
			},
			"deploy-gcp": {
				Name:        "deploy-gcp",
				Description: "Deploy to Google Cloud Run",
				WhenToUse:   "Use when deploying to Google Cloud",
				Enabled:     true,
			},
			"code-review": {
				Name:        "code-review",
				Description: "Review code changes for quality",
				WhenToUse:   "Use when reviewing a PR",
				Enabled:     true,
			},
			"api-docs": {
				Name:        "api-docs",
				Description: "Generate API documentation",
				Enabled:     true,
			},
			"disabled-skill": {
				Name:        "disabled-skill",
				Description: "Should not appear",
				Enabled:     false,
			},
		},
	}
}

func TestSkillSearch_KeywordMatch(t *testing.T) {
	tool := SkillTool{
		Skills:     makeFakeSkills(),
		NameLister: makeFakeSkills(),
	}
	input, _ := json.Marshal(map[string]string{
		"skill":       "?deploy",
		"description": "test",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "deploy-aws") {
		t.Errorf("expected deploy-aws in results, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "deploy-gcp") {
		t.Errorf("expected deploy-gcp in results, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "code-review") {
		t.Errorf("code-review should not appear in deploy search, got: %s", result.Content)
	}
}

func TestSkillSearch_EmptyQuery(t *testing.T) {
	tool := SkillTool{
		Skills:     makeFakeSkills(),
		NameLister: makeFakeSkills(),
	}
	input, _ := json.Marshal(map[string]string{
		"skill":       "?",
		"description": "test",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Available skills") {
		t.Errorf("expected all skills header, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "code-review") {
		t.Errorf("expected code-review in full listing, got: %s", result.Content)
	}
}

func TestSkillSearch_NoMatch(t *testing.T) {
	tool := SkillTool{
		Skills:     makeFakeSkills(),
		NameLister: makeFakeSkills(),
	}
	input, _ := json.Marshal(map[string]string{
		"skill":       "?nonexistent",
		"description": "test",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success (informational result), got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "No skills found") {
		t.Errorf("expected no match message, got: %s", result.Content)
	}
}

func TestSkillSearch_ExactNameRanking(t *testing.T) {
	skills := fakeSkillLookup{
		skills: map[string]*commands.Command{
			"deploy": {
				Name:        "deploy",
				Description: "Deploy application",
				Enabled:     true,
			},
			"deploy-advanced": {
				Name:        "deploy-advanced",
				Description: "Advanced deploy options",
				Enabled:     true,
			},
		},
	}
	tool := SkillTool{
		Skills:     skills,
		NameLister: skills,
	}
	input, _ := json.Marshal(map[string]string{
		"skill":       "?deploy",
		"description": "test",
	})
	result, _ := tool.Execute(t.Context(), input)
	// Exact match "deploy" should rank before "deploy-advanced"
	deployIdx := strings.Index(result.Content, "- deploy:")
	advancedIdx := strings.Index(result.Content, "deploy-advanced")
	if deployIdx < 0 || advancedIdx < 0 {
		t.Fatalf("expected both skills in results, got: %s", result.Content)
	}
	if deployIdx > advancedIdx {
		t.Errorf("exact match should rank first, got: %s", result.Content)
	}
}

func TestSkillSearch_NilLister(t *testing.T) {
	tool := SkillTool{
		Skills:     makeFakeSkills(),
		NameLister: nil,
	}
	input, _ := json.Marshal(map[string]string{
		"skill":       "?deploy",
		"description": "test",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error when lister is nil")
	}
}

func TestSkillSearch_MaxResults(t *testing.T) {
	skills := fakeSkillLookup{skills: map[string]*commands.Command{}}
	for i := 0; i < 30; i++ {
		skills.skills[string(rune('a'+i))] = &commands.Command{
			Name:        string(rune('a' + i)),
			Description: "test skill",
			Enabled:     true,
		}
	}
	tool := SkillTool{
		Skills:     skills,
		NameLister: skills,
	}
	input, _ := json.Marshal(map[string]string{
		"skill":       "?",
		"description": "test",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !strings.Contains(result.Content, "more") {
		t.Errorf("expected truncation message for >25 results, got: %s", result.Content)
	}
}
