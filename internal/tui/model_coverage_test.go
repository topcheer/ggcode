package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/util"
)

func TestStripImagePlaceholder(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		placeholder string
		want        string
	}{
		{"empty value returns empty", "", "[img]", ""},
		{"empty placeholder returns value", "hello", "", "hello"},
		{"exact match clears", "[image.png]", "[image.png]", ""},
		{"prefix stripped", "[image.png] hello world", "[image.png]", "hello world"},
		{"not a prefix keeps value", "hello [image.png]", "[image.png]", "hello [image.png]"},
		{"whitespace trimmed", "  [image.png] hello  ", "[image.png]", "hello"},
		{"placeholder not present", "some text", "[img]", "some text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripImagePlaceholder(tt.value, tt.placeholder)
			if got != tt.want {
				t.Errorf("stripImagePlaceholder(%q, %q) = %q, want %q", tt.value, tt.placeholder, got, tt.want)
			}
		})
	}
}

func TestLooksLikeStartupGarbage(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{

		{"ping", false},
		{"hello", false},
		{"Hello World", false},
		{"my-command", false},
		{"search_query", false},
		{"abc123", false},
		{"", false},

		{"11;rgb:0000/0000/0000", true},
		{"]11;rgb", true},
		{"1;1R", true},
		{"?2026;2$y", true},
		{"0;93;43m", true},
		{";rgb:", true},
		{"$y", true},
	}
	for _, tt := range tests {
		got := looksLikeStartupGarbage(tt.input)
		if got != tt.want {
			t.Errorf("looksLikeStartupGarbage(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestStartupInputSuppressionActive(t *testing.T) {

	if startupInputSuppressionActive(time.Time{}) {
		t.Error("expected inactive for zero time")
	}

	if !startupInputSuppressionActive(time.Now()) {
		t.Error("expected active for current time")
	}

	old := time.Now().Add(-1 * time.Second)
	if startupInputSuppressionActive(old) {
		t.Error("expected inactive for time > 500ms ago")
	}
}

func TestCombineCmds(t *testing.T) {

	if result := combineCmds(); result != nil {
		t.Error("expected nil for no cmds")
	}

	if result := combineCmds(nil, nil, nil); result != nil {
		t.Error("expected nil for all-nil cmds")
	}

	cmd := func() tea.Msg { return "hello" }
	if result := combineCmds(nil, cmd, nil); result == nil {
		t.Error("expected non-nil for single cmd")
	}

	cmd2 := func() tea.Msg { return "world" }
	if result := combineCmds(cmd, cmd2); result == nil {
		t.Error("expected non-nil for multiple cmds")
	}
}

func TestTruncateString_Model(t *testing.T) {

	if got := util.Truncate("hello world", 5); got != "he..." {
		t.Errorf("truncateString = %q, want %q", got, "he...")
	}
	if got := util.Truncate("hi", 100); got != "hi" {
		t.Errorf("truncateString = %q, want %q", got, "hi")
	}
	if got := util.Truncate("hello world", 5); got != "he..." {
		t.Errorf("truncateStr = %q, want %q", got, "he...")
	}
	if got := util.Truncate("", 5); got != "" {
		t.Errorf("truncateStr = %q, want %q", got, "")
	}
}

func TestPolicyMode(t *testing.T) {

	if got := policyMode(nil); got != permission.SupervisedMode {
		t.Errorf("policyMode(nil) = %v, want SupervisedMode", got)
	}

	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutoMode)
	if got := policyMode(policy); got != permission.AutoMode {
		t.Errorf("policyMode(auto) = %v, want AutoMode", got)
	}

	policy2 := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	if got := policyMode(policy2); got != permission.SupervisedMode {
		t.Errorf("policyMode(supervised) = %v, want SupervisedMode", got)
	}

	policy3 := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode)
	if got := policyMode(policy3); got != permission.AutopilotMode {
		t.Errorf("policyMode(autopilot) = %v, want AutopilotMode", got)
	}
}

func TestDefaultApprovalOptionsFor(t *testing.T) {
	en := defaultApprovalOptionsFor(LangEnglish)
	if len(en) != 3 {
		t.Fatalf("expected 3 English approval options, got %d", len(en))
	}

	if en[0].decision != permission.Allow {
		t.Errorf("expected first option Allow, got %v", en[0].decision)
	}

	if en[0].shortcut != "y" || en[2].shortcut != "n" {
		t.Errorf("expected shortcuts y/a/n, got %s/%s/%s", en[0].shortcut, en[1].shortcut, en[2].shortcut)
	}

	zh := defaultApprovalOptionsFor(LangZhCN)
	if len(zh) != 3 {
		t.Fatalf("expected 3 Chinese approval options, got %d", len(zh))
	}

	if zh[0].label == en[0].label {
		t.Error("expected Chinese labels to differ from English")
	}
}

func TestDiffConfirmOptionsFor(t *testing.T) {
	en := diffConfirmOptionsFor(LangEnglish)
	if len(en) != 2 {
		t.Fatalf("expected 2 English diff options, got %d", len(en))
	}
	if en[0].decision != permission.Allow {
		t.Errorf("expected first option Allow, got %v", en[0].decision)
	}
	if en[1].decision != permission.Deny {
		t.Errorf("expected second option Deny, got %v", en[1].decision)
	}

	zh := diffConfirmOptionsFor(LangZhCN)
	if len(zh) != 2 {
		t.Fatalf("expected 2 Chinese diff options, got %d", len(zh))
	}
	if zh[0].label == en[0].label {
		t.Error("expected Chinese diff labels to differ from English")
	}
}

func TestVendorNames_NilConfig(t *testing.T) {
	m := NewModel(nil, nil)
	if got := m.vendorNames(); got != "" {
		t.Errorf("expected empty string for nil config, got %+v", got)
	}
}

func TestVendorNames_WithConfig(t *testing.T) {
	cfg := &config.Config{
		Vendors: map[string]config.VendorConfig{
			"zai":       {},
			"anthropic": {},
		},
	}
	m := NewModel(nil, nil)
	m.SetConfig(cfg)
	got := m.vendorNames()
	if len(got) == 0 {
		t.Fatal("expected non-empty vendor names")
	}

	if !strings.Contains(got, "zai") || !strings.Contains(got, "anthropic") {
		t.Errorf("expected vendor names to contain 'zai' and 'anthropic', got %+v", got)
	}
}

func TestSetActiveRuntimeSelection(t *testing.T) {
	m := NewModel(nil, nil)
	m.setActiveRuntimeSelection("  zai  ", "  cn-coding  ", "  gpt-4  ")
	if m.activeVendor != "zai" {
		t.Errorf("expected vendor 'zai', got %q", m.activeVendor)
	}
	if m.activeEndpoint != "cn-coding" {
		t.Errorf("expected endpoint 'cn-coding', got %q", m.activeEndpoint)
	}
	if m.activeModel != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", m.activeModel)
	}
}

func TestAsciiLogo(t *testing.T) {
	logo := asciiLogo()
	if logo == "" {
		t.Fatal("expected non-empty ASCII logo")
	}

	if !strings.Contains(logo, "____") {
		t.Errorf("expected ASCII art patterns in logo, got %q", logo)
	}

	if !strings.HasSuffix(logo, "\n") {
		t.Error("expected logo to end with newline")
	}
}

func TestEnqueueAndConsumePendingSubmission(t *testing.T) {
	m := newTestModel()

	if m.pendingSubmissionCount() != 0 {
		t.Fatal("expected 0 pending submissions initially")
	}

	count := m.pending.enqueue("first message")
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
	m.pending.enqueue("second message")
	if m.pendingSubmissionCount() != 2 {
		t.Fatalf("expected count 2, got %d", m.pendingSubmissionCount())
	}

	snap := m.pendingSubmissionSnapshot()
	if len(snap) != 2 || snap[0] != "first message" || snap[1] != "second message" {
		t.Fatalf("unexpected snapshot: %v", snap)
	}

	joined := m.consumePendingSubmission()
	if joined != "first message\n\nsecond message" {
		t.Fatalf("unexpected joined result: %q", joined)
	}
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("expected 0 after consume, got %d", m.pendingSubmissionCount())
	}
}

func TestClearPendingSubmissions(t *testing.T) {
	m := newTestModel()
	m.pending.enqueue("a")
	m.pending.enqueue("b")
	m.clearPendingSubmissions()
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("expected 0 after clear, got %d", m.pendingSubmissionCount())
	}
}

func TestPendingSubmissionSnapshot_Empty(t *testing.T) {
	m := newTestModel()
	if snap := m.pendingSubmissionSnapshot(); snap != nil {
		t.Fatalf("expected nil for empty, got %v", snap)
	}
}

func TestRestorePendingInput_PendingOnly(t *testing.T) {
	m := newTestModel()
	m.pending.enqueue("queued text")
	m.restorePendingInput()
	if m.input.Value() != "queued text" {
		t.Fatalf("expected input 'queued text', got %q", m.input.Value())
	}
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("expected pending cleared after restore, got %d", m.pendingSubmissionCount())
	}
}

func TestRestorePendingInput_MergesWithDraft(t *testing.T) {
	m := newTestModel()
	m.pending.enqueue("queued")
	m.input.SetValue("draft")
	m.restorePendingInput()
	got := m.input.Value()

	if !strings.Contains(got, "queued") || !strings.Contains(got, "draft") {
		t.Fatalf("expected both 'queued' and 'draft' in input, got %+v", got)
	}
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("expected pending cleared after restore, got %d", m.pendingSubmissionCount())
	}
}

func TestRestorePendingInput_EmptyNoop(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("existing")
	m.restorePendingInput()
	if m.input.Value() != "existing" {
		t.Fatalf("expected unchanged input, got %q", m.input.Value())
	}
}

func TestPendingImages_AppendAndCount(t *testing.T) {
	m := NewModel(nil, nil)
	if m.pendingImageCount() != 0 {
		t.Fatalf("expected 0 images, got %d", m.pendingImageCount())
	}
	m.pendingImages = append(m.pendingImages, imageAttachedMsg{filename: "a.png"})
	m.pendingImages = append(m.pendingImages, imageAttachedMsg{filename: "b.png"})
	if m.pendingImageCount() != 2 {
		t.Fatalf("expected 2 images, got %d", m.pendingImageCount())
	}
}

func TestPendingImages_PopLast(t *testing.T) {
	m := NewModel(nil, nil)
	m.pendingImages = append(m.pendingImages, imageAttachedMsg{filename: "a.png"})
	m.pendingImages = append(m.pendingImages, imageAttachedMsg{filename: "b.png"})
	img, ok := m.popPendingImage()
	if !ok || img.filename != "b.png" {
		t.Fatalf("expected b.png, got %v %v", img, ok)
	}
	if m.pendingImageCount() != 1 {
		t.Fatalf("expected 1 remaining, got %d", m.pendingImageCount())
	}
}

func TestPendingImages_Clear(t *testing.T) {
	m := NewModel(nil, nil)
	m.pendingImages = append(m.pendingImages, imageAttachedMsg{filename: "a.png"})
	m.clearPendingImages()
	if m.pendingImageCount() != 0 {
		t.Fatalf("expected 0 after clear, got %d", m.pendingImageCount())
	}
}

func TestCancelActiveRun_SetsCanceledFlag(t *testing.T) {
	m := newTestModel()
	m.loading = true
	cancelCalled := false
	m.cancelFunc = func() { cancelCalled = true }

	m.cancelActiveRun()

	if !m.runCanceled {
		t.Error("expected runCanceled to be true")
	}
	if !cancelCalled {
		t.Error("expected cancelFunc to be called")
	}
	if !m.loading {
		t.Error("expected loading to remain true while cancellation unwinds")
	}
	if m.statusActivity != m.t("status.cancelling") {
		t.Errorf("expected cancelling statusActivity, got %q", m.statusActivity)
	}
}

func TestCancelActiveRun_Idempotent(t *testing.T) {
	m := newTestModel()
	m.loading = true
	cancelCount := 0
	m.cancelFunc = func() { cancelCount++ }

	m.cancelActiveRun()
	m.cancelActiveRun()

	if cancelCount != 1 {
		t.Fatalf("expected cancelFunc called once, got %d", cancelCount)
	}
}

func TestCancelActiveRun_NilCancelFunc(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.cancelFunc = nil

	m.cancelActiveRun()

	if m.runCanceled {
		t.Error("runCanceled must stay false when nothing is cancellable (cross-run poisoning)")
	}
}

func TestCancelActiveRun_FinalizesRunningToolAsCancelled(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.cancelFunc = func() {}
	m.chatStartTool(ToolStatusMsg{
		ToolID:      "tool-1",
		ToolName:    "read_file",
		DisplayName: "Read File",
		Detail:      "a.txt",
		Running:     true,
	})

	m.cancelActiveRun()

	item := m.chatList.FindByID("tool-1")
	if item == nil {
		t.Fatal("expected tool item to exist")
	}
	toolItem, ok := item.(interface {
		Status() chat.ToolStatus
		Result() string
		IsError() bool
	})
	if !ok {
		t.Fatal("expected tool item accessor")
	}
	if toolItem.Status() != chat.StatusCanceled {
		t.Fatalf("expected cancelled status, got %v", toolItem.Status())
	}
	if toolItem.Result() != "Cancelled" {
		t.Fatalf("expected cancelled result, got %q", toolItem.Result())
	}
	if !toolItem.IsError() {
		t.Fatal("expected cancelled tool item to be marked as error")
	}
}

func TestResetExitConfirm(t *testing.T) {
	m := newTestModel()
	m.exitConfirmPending = true
	m.resetExitConfirm()
	if m.exitConfirmPending {
		t.Error("expected exitConfirmPending to be false")
	}
}

func TestCloseActivePanel_NoPanel(t *testing.T) {
	m := newTestModel()
	if m.closeActivePanel() {
		t.Error("expected false when no panel is active")
	}
}

func TestCloseActivePanel_ModelPanel(t *testing.T) {
	m := newTestModel()
	m.modelPanel = &modelPanelState{}
	if !m.closeActivePanel() {
		t.Error("expected true when model panel is active")
	}
	if m.modelPanel != nil {
		t.Error("expected modelPanel to be nil after close")
	}
}

func TestCloseActivePanel_InspectorPanel(t *testing.T) {
	m := newTestModel()
	m.inspectorPanel = &inspectorPanelState{}
	if !m.closeActivePanel() {
		t.Error("expected true when inspector panel is active")
	}
	if m.inspectorPanel != nil {
		t.Error("expected inspectorPanel to be nil after close")
	}
}

func TestCloseActivePanel_MCPPanel(t *testing.T) {
	m := newTestModel()
	m.mcpPanel = &mcpPanelState{}
	if !m.closeActivePanel() {
		t.Error("expected true when MCP panel is active")
	}
	if m.mcpPanel != nil {
		t.Error("expected mcpPanel to be nil after close")
	}
}

func TestCloseActivePanel_SkillsPanel(t *testing.T) {
	m := newTestModel()
	m.skillsPanel = &skillsPanelState{}
	if !m.closeActivePanel() {
		t.Error("expected true when skills panel is active")
	}
	if m.skillsPanel != nil {
		t.Error("expected skillsPanel to be nil after close")
	}
}

func TestDrainPendingInterrupt_Empty(t *testing.T) {
	m := newTestModel()
	got := m.drainPendingInterrupt(42)
	if len(got) != 0 {
		t.Fatalf("expected empty string, got %+v", got)
	}
}

func TestDrainPendingInterrupt_WithPending(t *testing.T) {
	m := newTestModel()
	m.pending.enqueue("user interrupt text")
	got := m.drainPendingInterrupt(1)

	if len(got) != 1 || got[0].Type != "text" || got[0].Text != "user interrupt text" {
		t.Fatalf("expected one text block, got %+v", got)
	}
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("expected pending cleared, got %d", m.pendingSubmissionCount())
	}
}

func TestDrainPendingInterrupt_HiddenPendingStaysHidden(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.pending.enqueueHidden("scheduled hidden prompt", nil)

	got := m.drainPendingInterrupt(1)
	if len(got) != 1 || got[0].Text != "scheduled hidden prompt" {
		t.Fatalf("expected hidden prompt, got %+v", got)
	}
	if len(m.session.Messages) != 0 {
		t.Fatalf("expected hidden interrupt to skip session user message, got %d messages", len(m.session.Messages))
	}
	for _, ev := range m.session.TunnelEvents {
		if ev.Type == tunnel.EventUserMessage {
			t.Fatalf("expected hidden interrupt to skip tunnel user_message, got %+v", ev)
		}
	}
}

func TestPendingQueueConsumeDetailedKeepsHiddenEntriesSeparate(t *testing.T) {
	var q pendingQueue
	q.enqueue("visible one")
	q.enqueueHidden("hidden cron", nil)
	q.enqueue("visible two")

	text, hidden, override, _, _ := q.consumeDetailed()
	if text != "visible one" || hidden || override != nil {
		t.Fatalf("unexpected first consume: text=%q hidden=%v override=%+v", text, hidden, override)
	}
	text, hidden, override, _, _ = q.consumeDetailed()
	if text != "hidden cron" || !hidden || override != nil {
		t.Fatalf("unexpected second consume: text=%q hidden=%v override=%+v", text, hidden, override)
	}
	text, hidden, override, _, _ = q.consumeDetailed()
	if text != "visible two" || hidden || override != nil {
		t.Fatalf("unexpected third consume: text=%q hidden=%v override=%+v", text, hidden, override)
	}
}

func TestPendingQueueIsPointerShared(t *testing.T) {
	m := newTestModel()
	q := m.pending
	if q == nil {
		t.Fatal("expected pending queue to be initialized")
	}

	m2 := m
	if m2.pending != q {
		t.Fatal("expected Model copy to share the same pendingQueue pointer")
	}

	m2.pending.enqueue("hello")
	if q.count() != 1 || q.items[0].Text != "hello" {
		t.Fatal("expected enqueue on copy to be visible on original")
	}
}

func TestSessionMutexLazyInit(t *testing.T) {
	m := Model{}
	mu := m.sessionMutex()
	if mu == nil {
		t.Fatal("expected non-nil mutex after lazy init")
	}
	if m.sessionMutex() != mu {
		t.Fatal("expected same mutex instance")
	}
}

func TestCronPromptIdleSubmitsImmediately(t *testing.T) {
	m := newTestModel()
	m.loading = false

	msg := cronPromptMsg{Prompt: "check progress"}
	next, cmd := m.Update(msg)
	m2 := next.(Model)

	if cmd == nil {
		t.Fatal("expected non-nil cmd when cron fires while agent is idle — prompt should be submitted immediately")
	}

	if m2.chatList == nil || m2.chatList.Len() == 0 {
		t.Fatal("expected cron.firing system message in chatList")
	}

	if !m2.loading {
		t.Error("expected loading=true after cron prompt submission")
	}
	if len(m2.history) != 0 {
		t.Fatalf("expected hidden cron prompt to stay out of input history, got %#v", m2.history)
	}
}

func TestCronPromptBusyQueuesForLater(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 1

	msg := cronPromptMsg{Prompt: "check progress", QueueIfBusy: true}
	next, cmd := m.Update(msg)
	m2 := next.(Model)

	if cmd != nil {
		t.Fatal("expected nil cmd when cron fires while agent is busy — prompt should be queued")
	}

	if m2.pendingSubmissionCount() != 1 {
		t.Fatalf("expected 1 pending submission, got %d", m2.pendingSubmissionCount())
	}

	if m2.chatList == nil || m2.chatList.Len() == 0 {
		t.Fatal("expected cron.firing system message in chatList")
	}

	if !m2.loading {
		t.Error("expected loading=true to remain unchanged")
	}
}

func TestCronPromptBusyStaysHiddenWhenQueuedRunStarts(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 1

	next, _ := m.Update(cronPromptMsg{Prompt: "check progress", QueueIfBusy: true})
	m2 := next.(Model)

	nextModel, cmd := m2.handleAgentDoneMsg(agentDoneMsg{RunID: 1})
	m3 := nextModel

	if cmd == nil {
		t.Fatal("expected queued cron prompt to start a follow-up run")
	}
	if m3.chatList == nil || m3.chatList.Len() != 1 {
		t.Fatalf("expected only cron system message in chatList, got %d items", m3.chatList.Len())
	}
	if rendered := renderedOutput(&m3); strings.Contains(rendered, "check progress") {
		t.Fatalf("expected cron prompt to stay hidden, got output %q", rendered)
	}
	if !m3.loading {
		t.Fatal("expected follow-up hidden cron run to set loading=true")
	}
}

func TestCronPromptEmptyPromptDoesNothing(t *testing.T) {
	m := newTestModel()
	m.loading = false

	msg := cronPromptMsg{Prompt: ""}
	next, _ := m.Update(msg)
	m2 := next.(Model)

	if m2.chatList == nil || m2.chatList.Len() == 0 {
		t.Fatal("expected cron.firing system message even for empty prompt")
	}
}

func TestShutdownAll_NilManagers(t *testing.T) {
	m := newTestModel()

	m.shutdownAll()
}

func TestShutdownAll_WithSubAgents(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	m.subAgentMgr.Spawn("a1", "a1", "task1", nil, ctx1)
	m.subAgentMgr.Spawn("a2", "a2", "task2", nil, ctx2)
	m.subAgentMgr.SetCancel("sa-1", cancel1)
	m.subAgentMgr.SetCancel("sa-2", cancel2)

	m.shutdownAll()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sa1, ok1 := m.subAgentMgr.Get("sa-1")
		sa2, ok2 := m.subAgentMgr.Get("sa-2")
		if ok1 && sa1.CurrentStatus() == subagent.StatusCancelled &&
			ok2 && sa2.CurrentStatus() == subagent.StatusCancelled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	sa1, _ := m.subAgentMgr.Get("sa-1")
	sa2, _ := m.subAgentMgr.Get("sa-2")
	if sa1.Status != subagent.StatusCancelled {
		t.Errorf("expected sa-1 cancelled, got %s", sa1.Status)
	}
	if sa2.Status != subagent.StatusCancelled {
		t.Errorf("expected sa-2 cancelled, got %s", sa2.Status)
	}
}

func TestCancelActiveRun_CancelsSubAgents(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.cancelFunc = func() {}

	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.subAgentMgr.Spawn("a1", "a1", "task1", nil, ctx)
	m.subAgentMgr.SetCancel("sa-1", cancel)

	m.cancelActiveRun()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, ok := m.subAgentMgr.SnapshotByID("sa-1")
		if ok && snap.Status == subagent.StatusCancelled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap, ok := m.subAgentMgr.SnapshotByID("sa-1")
	if !ok || snap.Status != subagent.StatusCancelled {
		if ok {
			t.Errorf("expected sub-agent cancelled after cancelActiveRun, got %s", snap.Status)
		} else {
			t.Errorf("expected sub-agent cancelled after cancelActiveRun, agent not found")
		}
	}
}

func TestPendingQueuePreservesImagesThroughConsume(t *testing.T) {
	var q pendingQueue
	imgs := []imageAttachedMsg{{placeholder: "[img1]", filename: "test.png"}}

	q.enqueueWithImages("hello with image", imgs)
	if q.count() != 1 {
		t.Fatalf("expected 1 pending, got %d", q.count())
	}

	if len(q.items) != 1 || len(q.items[0].Images) != 1 {
		t.Fatalf("expected images stored in q.items[0], got items=%d imgs=%d",
			len(q.items), func() int {
				if len(q.items) > 0 {
					return len(q.items[0].Images)
				}
				return -1
			}())
	}

	text, hidden, override, consumedImgs, _ := q.consumeDetailed()
	if text != "hello with image" {
		t.Fatalf("unexpected text: %q", text)
	}
	if hidden || override != nil {
		t.Fatalf("unexpected hidden/override: hidden=%v override=%v", hidden, override)
	}
	if len(consumedImgs) != 1 || consumedImgs[0].filename != "test.png" {
		t.Fatalf("expected 1 image 'test.png', got %d images", len(consumedImgs))
	}
}
