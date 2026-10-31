package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// C-behavioral (harness mirrored from TestLanChatSendStarTriggersBroadcast):
// a MIXED list carrying "*" must take the broadcast path - the old code
// dropped the "*" into unresolved and sent only to named nodes.
func TestIssue4553C_MixedListWildcardBroadcastsBehavioral(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
	})
	result, err := tool.doSend(context.Background(), "hello", []string{"*", "node-bob"}, false, "")
	if err != nil {
		t.Fatalf("doSend with '*,node-bob' returned error: %v", err)
	}
	if strings.Contains(result.Content, "could not be resolved") || strings.Contains(result.Content, "unresolved") {
		t.Fatalf("mixed list must broadcast, not skip '*' as unresolved: %s", result.Content)
	}
	if result.IsError && strings.Contains(result.Content, "rate-limited") {
		t.Errorf("broadcast path must not be rate-limited: %s", result.Content)
	}
}
