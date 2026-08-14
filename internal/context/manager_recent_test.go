package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestBuildSummaryPlan_LastGroupOverBudget tests issue #324: when the last
// interaction group alone exceeds maxRecentGroupTokenRatio of the context
// window (e.g. a huge tool_result), the recent-retention loop used to break
// at i=0, leaving recentCount=0 and recentMsgs=nil — the current user request
// was then folded into the lossy summary. The fix guarantees the last group
// is kept verbatim even over budget.
func TestBuildSummaryPlan_LastGroupOverBudget(t *testing.T) {
	// Tiny window: maxRecentTokens = 1000 * 0.15 = 150 tokens (~600 chars).
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})

	// Two small earlier groups (so len(groups) > minRecentGroups).
	for i := 0; i < 2; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("q", 100)}}})
		cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("a", 100)}}})
	}

	// Last group: small user request + oversized tool_result (~12KB > budget).
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
	// The recent window must contain the verbatim user message of the last group.
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
	// Something must still be summarized (the earlier groups).
	if len(plan.oldMsgs) == 0 {
		t.Fatal("oldMsgs is empty: earlier groups should still be summarized")
	}
}

// TestBuildSummaryPlan_LastGroupWithinBudget verifies the normal path is
// unchanged: when the last group fits the budget it is kept, and the rest is
// summarized.
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
	// Last kept message must be the final assistant message.
	lastRecent := plan.recentMsgs[len(plan.recentMsgs)-1]
	if lastRecent.Role != "assistant" {
		t.Fatalf("expected last recent msg to be assistant, got %s", lastRecent.Role)
	}
}
