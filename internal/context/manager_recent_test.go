package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestBuildSummaryPlan_LastGroupOverBudget(t *testing.T) {
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})

	for i := 0; i < 2; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("q", 100)}}})
		cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("a", 100)}}})
	}

	lastUser := "FINAL VERBATIM USER TASK: fix the flaky login test"
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: lastUser}}})
	cm.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "toolResult",
			ToolName: "read_file",
			Output:   strings.Repeat("z", 12000),
		}},
	})

	plan, ok := cm.buildSummaryPlan()
	if !ok {
		t.Fatal("buildSummaryPlan returned ok=false")
	}
	if plan.recentMsgs == nil {
		t.Fatal("recentMsgs is nil: last group over budget must still be kept verbatim (issue #324 regression)")
	}
	found := false
	for _, msg := range plan.recentMsgs {
		if msg.Role == "user" {
			for _, b := range msg.Content {
				if b.Type == "text" && strings.Contains(b.Text, lastUser) {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("recentMsgs does not contain the verbatim last user message")
	}
	if len(plan.oldMsgs) == 0 {
		t.Fatal("oldMsgs is empty: earlier groups should still be summarized")
	}
}

func TestBuildSummaryPlan_LastGroupWithinBudget(t *testing.T) {
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})

	for i := 0; i < 3; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("q", 100)}}})
		cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("a", 100)}}})
	}

	plan, ok := cm.buildSummaryPlan()
	if !ok {
		t.Fatal("buildSummaryPlan returned ok=false")
	}
	if plan.recentMsgs == nil || len(plan.recentMsgs) == 0 {
		t.Fatal("recentMsgs empty in normal-budget scenario (behavior regression)")
	}
	if len(plan.oldMsgs) == 0 {
		t.Fatal("oldMsgs empty: earlier groups should be summarized")
	}
	lastRecent := plan.recentMsgs[len(plan.recentMsgs)-1]
	if lastRecent.Role != "assistant" {
		t.Fatalf("expected last recent msg to be assistant, got %s", lastRecent.Role)
	}
}
