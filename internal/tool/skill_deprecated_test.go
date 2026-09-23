package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type depFakeNameLister struct{}

func (depFakeNameLister) SkillNames() []string { return []string{"old-flow", "fresh"} }

func TestSkillToolDeprecatedExecuteAdvisory(t *testing.T) {
	tool := SkillTool{
		Skills: stubSkillLookup{
			"old-flow": {Name: "old-flow", Template: "Do the old thing", Deprecated: true, ReplacedBy: "new-flow", Enabled: true},
			"new-flow": {Name: "new-flow", Template: "Do the new thing", Enabled: true},
		},
	}
	input, err := json.Marshal(map[string]string{"skill": "old-flow", "args": ""})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	found := false
	for _, m := range result.FollowUpMessages {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "[deprecated skill]") && strings.Contains(b.Text, "new-flow") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected deprecation advisory mentioning successor in follow-up messages, got %+v", result)
	}
}

func TestSkillToolActiveSkillNoAdvisory(t *testing.T) {
	tool := SkillTool{
		Skills: stubSkillLookup{
			"fresh": {Name: "fresh", Template: "Do the thing", Enabled: true},
		},
	}
	input, err := json.Marshal(map[string]string{"skill": "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	for _, m := range result.FollowUpMessages {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "[deprecated skill]") {
				t.Fatalf("active skill must not carry a deprecation advisory, got %q", b.Text)
			}
		}
	}
}

func TestSkillToolSearchShowsDeprecatedTag(t *testing.T) {
	tool := SkillTool{
		Skills: stubSkillLookup{
			"old-flow": {Name: "old-flow", Description: "Old workflow", Deprecated: true, Enabled: true},
		},
		NameLister: depFakeNameLister{},
	}
	result, err := tool.Execute(context.Background(), []byte(`{"skill":"?old"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if !strings.Contains(result.Content, "(deprecated)") {
		t.Fatalf("expected deprecated tag in search results, got %q", result.Content)
	}
}
