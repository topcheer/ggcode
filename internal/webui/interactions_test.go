package webui

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5865: the webui must surface ask-type gates and carry the answer back.

func TestInteractionHub_PublishPendingRespond(t *testing.T) {
	h := NewInteractionHub()
	got := make(chan InteractionEvent, 8)
	unsub := h.Subscribe(func(ev InteractionEvent) { got <- ev })
	defer unsub()

	h.PublishRequest(InteractionEvent{Kind: InteractionKindApproval, ID: "r1", ToolName: "run_command", Input: `{"command":"ls"}`})
	ev := <-got
	if ev.Type != InteractionTypeRequest || ev.Kind != InteractionKindApproval || ev.ID != "r1" || ev.ToolName != "run_command" {
		t.Fatalf("unexpected request event: %+v", ev)
	}
	if p := h.Pending(); len(p) != 1 || p[0].ID != "r1" {
		t.Fatalf("expected r1 pending, got %+v", p)
	}

	var seen InteractionResponse
	h.SetResponder(func(r InteractionResponse) { seen = r })
	if !h.Respond(InteractionResponse{ID: "r1", Decision: InteractionDecisionAllow}) {
		t.Fatal("respond to a pending request must succeed")
	}
	if seen.ID != "r1" || seen.Decision != InteractionDecisionAllow {
		t.Fatalf("responder got %+v", seen)
	}
	// still pending until the host publishes the result
	if p := h.Pending(); len(p) != 1 {
		t.Fatalf("request must stay pending until resolved, got %+v", p)
	}

	h.PublishResult(InteractionKindApproval, "r1", "allow")
	res := <-got
	if res.Type != InteractionTypeResolved || res.ID != "r1" || res.Decision != "allow" {
		t.Fatalf("unexpected resolved event: %+v", res)
	}
	if p := h.Pending(); len(p) != 0 {
		t.Fatalf("resolved request must leave the pending set, got %+v", p)
	}
	// stale / duplicate browser responses are refused
	if h.Respond(InteractionResponse{ID: "r1", Decision: "deny"}) {
		t.Fatal("stale response must be refused")
	}
	if h.Respond(InteractionResponse{ID: "nope", Decision: "deny"}) {
		t.Fatal("unknown id must be refused")
	}
}

type stubTUIAgent struct{}

func (stubTUIAgent) Messages() []provider.Message { return nil }

// A tab that connects while an approval is pending sees it immediately, its
// answer reaches the host responder, and it sees the resolution.
func TestChatWS_InteractionReplayRespondResolve(t *testing.T) {
	bridge := NewTUIChatBridge(stubTUIAgent{}, nil)
	answered := make(chan InteractionResponse, 1)
	bridge.SetInteractionResponder(func(r InteractionResponse) { answered <- r })
	bridge.PublishInteraction(InteractionEvent{Kind: InteractionKindApproval, ID: "w1", ToolName: "run_command", Input: `{"command":"rm -rf x"}`})

	s := NewServer(config.DefaultConfig())
	s.SetChatBridge(bridge)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))

	read := func() map[string]interface{} {
		t.Helper()
		var m map[string]interface{}
		if err := ws.ReadJSON(&m); err != nil {
			t.Fatalf("read: %v", err)
		}
		return m
	}
	// replayed pending request
	var req map[string]interface{}
	for {
		req = read()
		if req["type"] == "interaction" {
			break
		}
	}
	if req["kind"] != "approval" || req["id"] != "w1" || req["tool_name"] != "run_command" {
		t.Fatalf("unexpected replayed interaction: %v", req)
	}

	// the tab answers
	if err := ws.WriteJSON(map[string]interface{}{"type": "interaction_response", "id": "w1", "decision": "deny"}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-answered:
		if r.ID != "w1" || r.Decision != InteractionDecisionDeny {
			t.Fatalf("responder got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("responder was not called")
	}
	for {
		m := read()
		if m["type"] == "interaction_ack" {
			if m["id"] != "w1" {
				t.Fatalf("unexpected ack %v", m)
			}
			break
		}
	}

	// the host resolves (here: the TUI key won) -> every tab clears it
	bridge.ResolveInteraction(InteractionKindApproval, "w1", "deny")
	for {
		m := read()
		if m["type"] == "interaction_resolved" {
			if m["id"] != "w1" || m["decision"] != "deny" {
				t.Fatalf("unexpected resolution %v", m)
			}
			break
		}
	}

	// a late response for the resolved id is refused, not treated as chat
	if err := ws.WriteJSON(map[string]interface{}{"type": "interaction_response", "id": "w1", "decision": "allow"}); err != nil {
		t.Fatal(err)
	}
	for {
		m := read()
		if m["type"] == "error" {
			if !strings.Contains(fmt.Sprint(m["error"]), "no pending interaction") {
				t.Fatalf("unexpected error %v", m)
			}
			break
		}
	}
}

func TestParseInteractionResponse(t *testing.T) {
	if _, ok := parseInteractionResponse([]byte(`{"type":"user_message","text":"hi"}`)); ok {
		t.Fatal("user_message must not be parsed as an interaction response")
	}
	resp, ok := parseInteractionResponse([]byte(`{"type":"interaction_response","id":"a1","decision":"submitted","answers":[{"question_id":"q1","choice_ids":["c1"]}]}`))
	if !ok || resp.ID != "a1" || resp.Decision != "submitted" || len(resp.Answers) != 1 || resp.Answers[0].QuestionID != "q1" {
		t.Fatalf("unexpected parse result: %+v ok=%v", resp, ok)
	}
	// ask_user request payload round-trips the tool types the frontend needs
	raw, _ := json.Marshal(InteractionEvent{Type: InteractionTypeRequest, Kind: InteractionKindAskUser, ID: "a2",
		Request: &tool.AskUserRequest{Title: "T", Questions: []tool.AskUserQuestion{{ID: "q1", Title: "Q", Kind: tool.AskUserKindSingle, Choices: []tool.AskUserChoice{{ID: "c1", Label: "L"}}}}}})
	var back InteractionEvent
	if err := json.Unmarshal(raw, &back); err != nil || back.Request == nil || back.Request.Questions[0].Choices[0].ID != "c1" {
		t.Fatalf("ask_user payload does not round-trip: %v %s", err, raw)
	}
}
