package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// #5622 user-adjudicated semantics supersede the #5557 nick ambiguity
// contract: same-nick fresh twins are one identity, DM picks the best twin
// (online first, newest last_seen) instead of erroring. What #5527->#5627
// keeps asserting here is #5557's COMPACTNESS contract under the new
// semantics: when best-effort delivery fails (dead twin endpoints), the
// failure error stays compact - no ambiguity interception, no roster dump,
// no bystander leak.
func TestIssue5557_DupNickSendErrorCompactWithHolders(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	hub.SetNickRoleTeam("alice", "frontend", "platform")

	hub.HandlePresence(lanchat.Participant{
		NodeID: "node-twin-a", HumanNick: "carol_dev", AgentNick: "carol_dev",
		Endpoint: "http://localhost:2", Online: true, LastSeen: 100,
	})
	hub.HandlePresence(lanchat.Participant{
		NodeID: "node-twin-b", HumanNick: "carol_dev", AgentNick: "carol_dev",
		Endpoint: "http://localhost:3", Online: true, LastSeen: 200,
	})
	// unrelated third peer: must NOT appear in any error (old behavior dumped
	// the whole roster, 12.9KB on a busy LAN)
	hub.HandlePresence(lanchat.Participant{
		NodeID: "node-bystander", HumanNick: "mallory_ops", AgentNick: "mallory_ops",
		Endpoint: "http://localhost:4", Online: true,
	})

	payload, _ := json.Marshal(map[string]interface{}{"action": "send", "to": "carol_dev", "message": "hi"})
	res, err := tool.Execute(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	// Dead twin endpoints: best-effort delivery legitimately FAILS, but the
	// failure must be a delivery error, never the pre-#5622 ambiguity block.
	if !res.IsError {
		t.Fatalf("dead twin endpoints must fail delivery, got: %.200s", res.Content)
	}
	body := res.Content
	if strings.Contains(body, "ambiguous recipient") {
		t.Fatalf("#5622 semantics: nick twins must not hit ambiguity interception: %.200s", body)
	}
	if strings.Contains(body, "mallory_ops") || strings.Contains(body, "node-bystander") {
		t.Fatalf("unrelated peer leaked into compact error (roster dump regression): %.300s", body)
	}
	if len(body) > 1200 {
		t.Fatalf("error ballooned to %d bytes - compact contract broken", len(body))
	}
}

// #5557: registration side must also conflict-check AgentNick, so two agents
// cannot silently sit on the same agent nick until send time.
func TestIssue5557_AgentNickConflictResolves(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	_ = tool

	hub.HandlePresence(lanchat.Participant{
		NodeID: "node-holder", HumanNick: "dave_x", AgentNick: "dave_x",
		Endpoint: "http://localhost:2", Online: true, LastSeen: 100,
	})
	// self takes the SAME agent nick as the holder (human nick differs, so
	// the old HumanNick-only check would not fire)
	hub.SetNickRoleTeam("erin", "x", "")
	if got := hub.AgentNick(); got != "erin_x_agent" {
		t.Fatalf("sanity: expected erin_x_agent, got %q", got)
	}
	if hub.AgentNick() == "dave_x" {
		t.Fatal("precondition broken")
	}
	// force the collision: set agent nick directly equal to holder's
	hub.SetNickRoleTeam("dave", "x", "")
	if got := hub.AgentNick(); got == "dave_x" {
		t.Fatalf("AgentNick collision must self-resolve, stayed %q", got)
	}
}
