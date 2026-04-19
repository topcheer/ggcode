package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

func waitForAskUserResponse(t *testing.T, ch <-chan tool.AskUserResponse) tool.AskUserResponse {
	t.Helper()
	select {
	case resp := <-ch:
		return resp
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ask_user response")
		return tool.AskUserResponse{}
	}
}

type testIMSink struct {
	mu     sync.Mutex
	name   string
	events []im.OutboundEvent
}

func (s *testIMSink) Name() string { return s.name }

func (s *testIMSink) Send(_ context.Context, _ im.ChannelBinding, event im.OutboundEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *testIMSink) snapshot() []im.OutboundEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]im.OutboundEvent, len(s.events))
	copy(events, s.events)
	return events
}

type testStreamProvider struct {
	events []provider.StreamEvent
}

func (p *testStreamProvider) Name() string { return "test-stream" }

func (p *testStreamProvider) Chat(context.Context, []provider.Message, []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, nil
}

func (p *testStreamProvider) ChatStream(context.Context, []provider.Message, []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, len(p.events))
	for _, event := range p.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (p *testStreamProvider) CountTokens(context.Context, []provider.Message) (int, error) {
	return 0, nil
}

func TestSetSessionBindsIMRuntime(t *testing.T) {
	m := NewModel(nil, nil)
	imMgr := im.NewManager()
	m.SetIMManager(imMgr)

	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore returned error: %v", err)
	}
	ses := session.NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	m.SetSession(ses, store)

	active := imMgr.ActiveSession()
	if active == nil {
		t.Fatal("expected IM runtime to bind the active session")
	}
	if active.SessionID != ses.ID || active.Workspace != ses.Workspace {
		t.Fatalf("unexpected IM session binding: %#v", active)
	}
}

func TestAgentDoneDoesNotEmitBufferedIMMessageWithoutRoundEvent(t *testing.T) {
	m := NewModel(nil, nil)
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	m.loading = true
	m.activeAgentRunID = 1

	next, _ := m.Update(agentStreamMsg{RunID: 1, Text: "hello "})
	m = next.(Model)
	time.Sleep(20 * time.Millisecond)
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("expected no IM emits during streaming chunks, got %#v", events)
	}

	next, _ = m.Update(agentStreamMsg{RunID: 1, Text: "world"})
	m = next.(Model)
	time.Sleep(20 * time.Millisecond)
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("expected no IM emits during streaming chunks, got %#v", events)
	}

	next, _ = m.Update(agentDoneMsg{RunID: 1})
	m = next.(Model)
	events := sink.snapshot()
	if len(events) != 0 {
		t.Fatalf("expected no IM emit without round summary event, got %#v", events)
	}
}

func TestLivePromptEmitsSingleFinalIMText(t *testing.T) {
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)

	prov := &testStreamProvider{events: []provider.StreamEvent{
		{Type: provider.StreamEventText, Text: "hello "},
		{Type: provider.StreamEventText, Text: "world"},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{}},
	}}
	ag := agent.NewAgent(prov, tool.NewRegistry(), "", 0)
	m := NewModel(ag, permission.NewConfigPolicy(nil, nil))
	m.startedAt = time.Now().Add(-2 * time.Second)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	m.input.SetValue("ping")

	h := startLiveProgramHarness(t, m)
	defer h.close()
	h.send(tea.KeyPressMsg{Code: tea.KeyEnter})

	waitForProgramState(t, h, func(state Model) bool {
		return !state.loading
	})

	deadline := time.Now().Add(2 * time.Second)
	var events []im.OutboundEvent
	for time.Now().Before(deadline) {
		events = sink.snapshot()
		if len(events) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) == 0 {
		t.Fatal("expected IM events after live prompt submission")
	}

	texts := make([]im.OutboundEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == im.OutboundEventText {
			texts = append(texts, event)
		}
	}
	if len(texts) != 2 {
		t.Fatalf("expected mirrored user text plus assistant text, got %#v", events)
	}
	if texts[0].Text != "【用户】ping\n" {
		t.Fatalf("expected mirrored user IM text, got %q", texts[0].Text)
	}
	if texts[1].Text != "hello world" {
		t.Fatalf("expected merged live IM text, got %q", texts[1].Text)
	}
}

func TestAgentRoundSummaryEmitsRawAccumulatedText(t *testing.T) {
	m := NewModel(nil, nil)
	m.language = LangZhCN
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	m.loading = true
	m.activeAgentRunID = 1

	next, _ := m.Update(agentRoundSummaryMsg{
		RunID:         1,
		Text:          "\n\n# 标题\n\n- 条目一\n- 条目二\n",
		ToolCalls:     2,
		ToolSuccesses: 2,
		ToolFailures:  0,
	})
	m = next.(Model)

	deadline := time.Now().Add(500 * time.Millisecond)
	var events []im.OutboundEvent
	for time.Now().Before(deadline) {
		events = sink.snapshot()
		if len(events) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 1 {
		t.Fatalf("expected one IM text event, got %#v", events)
	}
	if events[0].Kind != im.OutboundEventText || events[0].Text != "\n\n# 标题\n\n- 条目一\n- 条目二\n" {
		t.Fatalf("unexpected IM summary event: %#v", events)
	}
}

func TestEscapeRejectsPendingIMPairing(t *testing.T) {
	imMgr := im.NewManager()
	if err := imMgr.SetBindingStore(im.NewMemoryBindingStore()); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	if err := imMgr.SetPairingStore(im.NewMemoryPairingStore()); err != nil {
		t.Fatalf("SetPairingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform: im.PlatformQQ,
		Adapter:  "qq",
		TargetID: "ops",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)

	m := NewModel(nil, nil)
	m.startedAt = time.Now().Add(-2 * time.Second)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	if _, err := imMgr.HandlePairingInbound(im.InboundMessage{
		Envelope: im.Envelope{
			Adapter:    "qq",
			Platform:   im.PlatformQQ,
			ChannelID:  "group-1",
			SenderID:   "user-1",
			MessageID:  "msg-1",
			ReceivedAt: time.Now(),
		},
		Text: "bind",
	}); err != nil {
		t.Fatalf("HandlePairingInbound returned error: %v", err)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected pairing rejection command")
	}
	msg := cmd()
	next, _ = m.Update(msg)
	m = next.(Model)

	if pending := imMgr.Snapshot().PendingPairing; pending != nil {
		t.Fatalf("expected pending pairing to clear, got %#v", pending)
	}
	events := sink.snapshot()
	if len(events) != 1 || !strings.Contains(events[0].Text, "拒绝") {
		t.Fatalf("expected rejection notice to be emitted, got %#v", events)
	}
}

func TestLocalInputEnterEmitsUserMirrorToIM(t *testing.T) {
	m := newTestModel()
	m.language = LangZhCN
	m.loading = true
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	m.input.SetValue("hello")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	deadline := time.Now().Add(500 * time.Millisecond)
	var events []im.OutboundEvent
	for time.Now().Before(deadline) {
		events = sink.snapshot()
		if len(events) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 1 || events[0].Kind != im.OutboundEventText || events[0].Text != "【用户】hello\n" {
		t.Fatalf("expected mirrored local user input, got %#v", events)
	}
}

func TestAskUserRoundEmitsExplicitQuestionMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.language = LangZhCN
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	m.loading = true
	m.activeAgentRunID = 1

	next, _ := m.Update(AskUserMsg{
		Request: tool.AskUserRequest{
			Title: "Clarify scope",
			Questions: []tool.AskUserQuestion{{
				Title:   "Need scope",
				Prompt:  "What scope should I use?",
				Kind:    tool.AskUserKindSingle,
				Choices: []tool.AskUserChoice{{Label: "small"}, {Label: "full"}},
			}},
		},
	})
	m = next.(Model)

	deadline := time.Now().Add(500 * time.Millisecond)
	var events []im.OutboundEvent
	for time.Now().Before(deadline) {
		events = sink.snapshot()
		if len(events) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 1 {
		t.Fatalf("expected one ask_user IM message, got %#v", events)
	}
	if events[0].Kind != im.OutboundEventText {
		t.Fatalf("expected ask_user to emit text, got %#v", events)
	}
	if events[0].Text != "📋 **Clarify scope**\n**What scope should I use?**\n  1. small\n  2. full\n  _（回复编号 1-2 或选项文本）_\n\n💬 回复编号或选项文本。" {
		t.Fatalf("unexpected ask_user IM message:\n%s", events[0].Text)
	}
}

func TestRemoteInboundAnswerSubmitsPendingQuestionnaire(t *testing.T) {
	m := NewModel(nil, nil)
	m.language = LangZhCN
	respCh := make(chan tool.AskUserResponse, 1)
	req := tool.AskUserRequest{
		Title: "选择 Review 范围",
		Questions: []tool.AskUserQuestion{{
			ID:     "scope",
			Title:  "Review 范围",
			Prompt: "请问你想 review 哪部分代码？",
			Kind:   tool.AskUserKindSingle,
			Choices: []tool.AskUserChoice{
				{ID: "latest", Label: "最新一次提交"},
				{ID: "recent", Label: "最近 3 次提交"},
			},
		}},
	}
	m.pendingQuestionnaire = newQuestionnaireState(req, respCh, m.currentLanguage())

	next, cmd := m.Update(remoteInboundMsg{
		Message: im.InboundMessage{Text: "1"},
	})
	m = next.(Model)

	if cmd != nil {
		cmd()
	}
	if m.pendingQuestionnaire != nil {
		t.Fatal("expected remote questionnaire answer to complete pending questionnaire")
	}
	resp := waitForAskUserResponse(t, respCh)
	if resp.Status != tool.AskUserStatusSubmitted {
		t.Fatalf("expected submitted status, got %#v", resp)
	}
	if len(resp.Answers) != 1 || len(resp.Answers[0].SelectedChoiceIDs) != 1 || resp.Answers[0].SelectedChoiceIDs[0] != "latest" {
		t.Fatalf("expected first choice to be selected, got %#v", resp)
	}
}

func TestRemoteInboundAnswerAdvancesQuestionnaireAndEmitsNextQuestion(t *testing.T) {
	m := NewModel(nil, nil)
	m.language = LangZhCN
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)

	respCh := make(chan tool.AskUserResponse, 1)
	req := tool.AskUserRequest{
		Title: "补充信息",
		Questions: []tool.AskUserQuestion{
			{
				ID:     "scope",
				Title:  "Review 范围",
				Prompt: "请问你想 review 哪部分代码？",
				Kind:   tool.AskUserKindSingle,
				Choices: []tool.AskUserChoice{
					{ID: "latest", Label: "最新一次提交"},
					{ID: "recent", Label: "最近 3 次提交"},
				},
			},
			{
				ID:            "notes",
				Title:         "补充说明",
				Prompt:        "还有别的要求吗？",
				Kind:          tool.AskUserKindText,
				AllowFreeform: true,
			},
		},
	}
	m.pendingQuestionnaire = newQuestionnaireState(req, respCh, m.currentLanguage())

	next, cmd := m.Update(remoteInboundMsg{
		Message: im.InboundMessage{Text: "1"},
	})
	m = next.(Model)

	if cmd != nil {
		t.Fatal("expected partial remote questionnaire answer not to submit yet")
	}
	if m.pendingQuestionnaire == nil {
		t.Fatal("expected questionnaire to remain pending for the second question")
	}
	if idx := m.pendingQuestionnaire.activeQuestionIndex(); idx != 1 {
		t.Fatalf("expected questionnaire to advance to second question, got %d", idx)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	var events []im.OutboundEvent
	for time.Now().Before(deadline) {
		events = sink.snapshot()
		if len(events) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 1 || events[0].Kind != im.OutboundEventText || !strings.Contains(events[0].Text, "还有别的要求吗") {
		t.Fatalf("expected next ask_user question to be emitted to IM, got %#v", events)
	}
}

func TestToolOnlyRoundsDoNotProduceIMSummary(t *testing.T) {
	round := agentIMRoundState{
		ToolCalls:     3,
		ToolSuccesses: 2,
		ToolFailures:  1,
	}

	if round.HasVisibleOutput() {
		t.Fatal("expected tool-only rounds to stay out of IM output")
	}
}

func TestSubAgentUpdateDoesNotEmitIMStatus(t *testing.T) {
	m := NewModel(nil, nil)
	m.language = LangZhCN
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	m.loading = true
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	id := m.subAgentMgr.Spawn("review-1", "Review core architecture", nil, context.Background())
	sa, ok := m.subAgentMgr.Get(id)
	if !ok {
		t.Fatal("expected spawned sub-agent to exist")
	}
	sa.CurrentTool = "git_status"
	sa.CurrentArgs = `{}`
	sa.CurrentPhase = "tool"

	next, _ := m.Update(subAgentUpdateMsg{})
	m = next.(Model)
	time.Sleep(30 * time.Millisecond)
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("expected sub-agent updates to stay out of IM round messaging, got %#v", events)
	}
}

func TestIMEmitterPreservesStatusThenTextOrder(t *testing.T) {
	m := NewModel(nil, nil)
	m.language = LangZhCN
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &testIMSink{name: "qq"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)

	m.emitIMStatus("正在检查 git status...")
	m.emitIMText("检查完成")

	deadline := time.Now().Add(500 * time.Millisecond)
	var events []im.OutboundEvent
	for time.Now().Before(deadline) {
		events = sink.snapshot()
		if len(events) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 2 {
		t.Fatalf("expected status then text events, got %#v", events)
	}
	if events[0].Kind != im.OutboundEventStatus || events[0].Status != "正在检查 git status..." {
		t.Fatalf("unexpected first IM event: %#v", events)
	}
	if events[1].Kind != im.OutboundEventText || events[1].Text != "检查完成" {
		t.Fatalf("unexpected second IM event: %#v", events)
	}
}
