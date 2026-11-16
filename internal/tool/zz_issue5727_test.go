package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

func newTool5727(t *testing.T) LanChatTool {
	t.Helper()
	hub := lanchat.NewHub("node-self", "tui", "http://localhost:1", "",
		lanchat.NewStore(t.TempDir()), lanchat.WorkspaceMeta{Workspace: "/tmp/test5727"})
	hub.UpdatePeers([]lanchat.Participant{
		{NodeID: "node-b", HumanNick: "bob_dev", Endpoint: "http://127.0.0.1:1"},
		{NodeID: "node-c", HumanNick: "carol_dev", Endpoint: "http://127.0.0.1:1"},
	})
	return LanChatTool{Hub: hub, rateLimiter: newAgentRateLimiter(), dmCooldown: time.Minute}
}

// action='receipt' with message_id lists one line per recipient and a
// summary verdict using the #4774 three-way vocabulary. One confirmed ACK
// plus one not-yet-ACKed recipient must read as PARTIAL, not success.
func TestIssue5727ReceiptActionPartialVerdict(t *testing.T) {
	tt := newTool5727(t)
	tt.Hub.HandleReceipt(lanchat.Receipt{MessageID: "msg-1", Status: lanchat.StatusDelivered, FromNodeID: "node-b", Timestamp: 1})
	tt.Hub.HandleReceipt(lanchat.Receipt{MessageID: "msg-1", Status: lanchat.StatusSent, FromNodeID: "node-c", Timestamp: 1})

	res := tt.doReceipt("msg-1")
	if res.IsError {
		t.Fatalf("receipt query errored: %s", res.Content)
	}
	for _, want := range []string{"bob_dev", "carol_dev", "delivered", "sent", "Partial delivery", "unknown 1/2"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("receipt output missing %q: %s", want, res.Content)
		}
	}
}

func TestIssue5727ReceiptActionAllConfirmedVerdict(t *testing.T) {
	tt := newTool5727(t)
	tt.Hub.HandleReceipt(lanchat.Receipt{MessageID: "msg-2", Status: lanchat.StatusDelivered, FromNodeID: "node-b", Timestamp: 1})
	tt.Hub.HandleReceipt(lanchat.Receipt{MessageID: "msg-2", Status: lanchat.StatusRejected, FromNodeID: "node-c", Reason: "auto-rejected by policy", Timestamp: 2})

	res := tt.doReceipt("msg-2")
	if res.IsError {
		t.Fatalf("receipt query errored: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Delivery confirmed by all 2 recipient(s)") {
		t.Errorf("all-confirmed verdict missing: %s", res.Content)
	}
	if !strings.Contains(res.Content, "auto-rejected by policy") {
		t.Errorf("rejection reason missing from per-recipient line: %s", res.Content)
	}
}

// action='receipt' without message_id lists recent recorded ids so a
// sender can discover what to query without keeping ids by hand.
func TestIssue5727ReceiptActionRecentList(t *testing.T) {
	tt := newTool5727(t)
	tt.Hub.HandleReceipt(lanchat.Receipt{MessageID: "recent-1", Status: lanchat.StatusDelivered, FromNodeID: "node-b"})

	res := tt.doReceipt("")
	if res.IsError {
		t.Fatalf("receipt listing errored: %s", res.Content)
	}
	if !strings.Contains(res.Content, "recent-1") || !strings.Contains(res.Content, "newest first") {
		t.Errorf("recent list missing id/status: %s", res.Content)
	}
}

func TestIssue5727ReceiptActionUnknownID(t *testing.T) {
	tt := newTool5727(t)
	res := tt.doReceipt("no-such-id")
	if !res.IsError {
		t.Fatalf("unknown message id should be an error result, got: %s", res.Content)
	}
}

// End-to-end wiring: a send (here to an unreachable peer, so the transport
// refuses) still names the per-recipient message id in its result, records
// a failed disposition, and the receipt query answers with that id.
func TestIssue5727SendResultNamesQueryableMessageID(t *testing.T) {
	tt := newTool5727(t)

	oldDelay := lanchat.PeerRetryDelayFn
	lanchat.PeerRetryDelayFn = func(int) time.Duration { return 0 }
	defer func() { lanchat.PeerRetryDelayFn = oldDelay }()

	input, err := json.Marshal(map[string]interface{}{
		"action": "send", "to": "bob_dev", "message": "hello",
		"as_agent": false, "description": "test send",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tt.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	const marker = "Receipt ids: bob_dev="
	idx := strings.Index(res.Content, marker)
	if idx < 0 {
		t.Fatalf("send result does not name the message id: %s", res.Content)
	}
	rest := res.Content[idx+len(marker):]
	end := strings.IndexAny(rest, "\n\r")
	if end < 0 {
		end = len(rest)
	}
	msgID := strings.TrimSpace(rest[:end])
	if msgID == "" {
		t.Fatal("empty message id in send result")
	}

	// The recent list surfaces it and the per-recipient query answers with
	// the failed disposition and a not-delivered verdict.
	if listRes := tt.doReceipt(""); !strings.Contains(listRes.Content, msgID) {
		t.Errorf("recent receipt list does not mention %s: %s", msgID, listRes.Content)
	}
	queryRes := tt.doReceipt(msgID)
	if queryRes.IsError {
		t.Fatalf("receipt query for just-sent id errored: %s", queryRes.Content)
	}
	if !strings.Contains(queryRes.Content, "failed") || !strings.Contains(queryRes.Content, "Not delivered") {
		t.Errorf("receipt query should show failed + Not delivered: %s", queryRes.Content)
	}
}

// The tool parameters must advertise the receipt action so model callers
// can discover it.
func TestIssue5727ParametersAdvertiseReceiptAction(t *testing.T) {
	tt := newTool5727(t)
	var spec struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(tt.Parameters()), &spec); err != nil {
		t.Fatalf("parameters schema is not valid JSON: %v", err)
	}
	found := false
	for _, a := range spec.Properties.Action.Enum {
		if a == "receipt" {
			found = true
		}
	}
	if !found {
		t.Errorf("receipt action missing from schema enum: %v", spec.Properties.Action.Enum)
	}
}
