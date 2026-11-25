package tui

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/webui"
)

// #5865: browser tabs answer ask-type gates like the mobile tunnel does.

type fakeWebUIBridge struct {
	published []webui.InteractionEvent
	resolved  []string
}

func (f *fakeWebUIBridge) BroadcastEvent(provider.StreamEvent) {}
func (f *fakeWebUIBridge) PublishInteraction(ev webui.InteractionEvent) {
	f.published = append(f.published, ev)
}
func (f *fakeWebUIBridge) ResolveInteraction(kind, id, decision string) {
	f.resolved = append(f.resolved, kind+":"+id+":"+decision)
}

func TestApprovalMsg_PushedToWebUI(t *testing.T) {
	m := newTestModel()
	fb := &fakeWebUIBridge{}
	m.webuiBridge = fb

	updated, _ := m.handleApprovalMsg(ApprovalMsg{ToolName: "run_command", Input: `{"command":"ls"}`})
	if updated.webUIPendingApprovalID == "" {
		t.Fatal("expected a webui request id")
	}
	if len(fb.published) != 1 {
		t.Fatalf("expected one published interaction, got %+v", fb.published)
	}
	ev := fb.published[0]
	if ev.Kind != webui.InteractionKindApproval || ev.ID != updated.webUIPendingApprovalID || ev.ToolName != "run_command" || ev.Input != `{"command":"ls"}` {
		t.Fatalf("unexpected published interaction: %+v", ev)
	}
}

func TestWebUIApprovalResponse_ResolvesPending(t *testing.T) {
	m := newTestModel()
	fb := &fakeWebUIBridge{}
	m.webuiBridge = fb
	resp := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{ToolName: "run_command", Response: resp}
	m.webUIPendingApprovalID = "webui-req-7"

	got, _ := m.handleWebUIApprovalResponse(webUIApprovalResponseMsg{id: "webui-req-7", decision: webui.InteractionDecisionAlwaysAllow})
	updated := got.(*Model)
	select {
	case d := <-resp:
		if d != permission.Allow {
			t.Fatalf("expected Allow, got %v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("approval was not answered")
	}
	if updated.pendingApproval != nil || updated.webUIPendingApprovalID != "" {
		t.Fatalf("pending approval must be cleared: %+v / %q", updated.pendingApproval, updated.webUIPendingApprovalID)
	}
	if len(fb.resolved) != 1 || fb.resolved[0] != "approval:webui-req-7:"+webui.InteractionDecisionAlwaysAllow {
		t.Fatalf("browser tabs must see the resolution, got %v", fb.resolved)
	}
}

func TestWebUIApprovalResponse_IgnoresMismatchedAndUnknown(t *testing.T) {
	m := newTestModel()
	fb := &fakeWebUIBridge{}
	m.webuiBridge = fb
	m.pendingApproval = &ApprovalMsg{ToolName: "run_command"}
	m.webUIPendingApprovalID = "webui-req-1"

	got, _ := m.handleWebUIApprovalResponse(webUIApprovalResponseMsg{id: "webui-req-2", decision: "allow"})
	if got.(*Model).pendingApproval == nil {
		t.Fatal("mismatched id must be ignored")
	}
	got, _ = m.handleWebUIApprovalResponse(webUIApprovalResponseMsg{id: "webui-req-1", decision: "yolo"})
	if got.(*Model).pendingApproval == nil {
		t.Fatal("unknown decision must be ignored")
	}
	got, _ = m.handleWebUIApprovalResponse(webUIApprovalResponseMsg{id: "", decision: "allow"})
	if got.(*Model).pendingApproval == nil {
		t.Fatal("empty id must be ignored")
	}
	if len(fb.resolved) != 0 {
		t.Fatalf("nothing should have been resolved, got %v", fb.resolved)
	}
}

func TestTUIApprovalKey_ResolvesWebUIToo(t *testing.T) {
	m := newTestModel()
	fb := &fakeWebUIBridge{}
	m.webuiBridge = fb
	m.pendingApproval = &ApprovalMsg{ToolName: "run_command"}
	m.webUIPendingApprovalID = "webui-req-3"

	m.handleApproval(permission.Deny)
	if len(fb.resolved) != 1 || fb.resolved[0] != "approval:webui-req-3:deny" {
		t.Fatalf("a TUI answer must clear the browser copy, got %v", fb.resolved)
	}
}

func TestWebUIAskUserResponse_ResolvesQuestionnaire(t *testing.T) {
	m := newTestModel()
	fb := &fakeWebUIBridge{}
	m.webuiBridge = fb
	resp := make(chan toolpkg.AskUserResponse, 1)
	req := toolpkg.AskUserRequest{Title: "Pick", Questions: []toolpkg.AskUserQuestion{
		{ID: "q1", Title: "Which?", Kind: toolpkg.AskUserKindSingle, Choices: []toolpkg.AskUserChoice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}},
	}}
	m.pendingQuestionnaire = newQuestionnaireState(req, resp, m.currentLanguage())
	m.webUIPendingAskUserID = "webui-req-9"

	got, _ := m.handleWebUIAskUserResponse(webUIAskUserResponseMsg{
		id:      "webui-req-9",
		status:  toolpkg.AskUserStatusSubmitted,
		answers: []tunnel.AskUserAnswer{{QuestionID: "q1", ChoiceIDs: []string{"b"}}},
	})
	updated := got.(*Model)
	select {
	case r := <-resp:
		if r.Status != toolpkg.AskUserStatusSubmitted || len(r.Answers) != 1 || !r.Answers[0].Answered || len(r.Answers[0].SelectedChoiceIDs) != 1 || r.Answers[0].SelectedChoiceIDs[0] != "b" {
			t.Fatalf("unexpected response: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("questionnaire was not answered")
	}
	if updated.pendingQuestionnaire != nil || updated.webUIPendingAskUserID != "" {
		t.Fatal("questionnaire must be cleared")
	}
	if len(fb.resolved) != 1 || fb.resolved[0] != "ask_user:webui-req-9:submitted" {
		t.Fatalf("browser tabs must see the resolution, got %v", fb.resolved)
	}
}
