package main

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestUIStateAppendAssistantTextEmitsChunkDeltas(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	ui := NewUIState()
	var events []UIEvent
	ui.OnEvent = func(e UIEvent) {
		events = append(events, e)
	}

	ui.AppendAssistantText("Hello")
	if len(events) != 1 || events[0].Type != EventAppend {
		t.Fatalf("events after first chunk = %#v, want a single append event", events)
	}
	if got := ui.ChatMsgs[0].Content; got != "Hello" {
		t.Fatalf("assistant content = %q, want %q", got, "Hello")
	}

	ui.streamLastNotify.Store(time.Now().Add(-time.Second).UnixMilli())
	ui.AppendAssistantText(" world")
	if len(events) != 2 || events[1].Type != EventAssistantChunk || events[1].Text != " world" {
		t.Fatalf("events after second chunk = %#v, want chunk delta", events)
	}
	if got := ui.ChatMsgs[0].Content; got != "Hello world" {
		t.Fatalf("assistant content = %q, want %q", got, "Hello world")
	}

	ui.AppendAssistantText("!")
	ui.FlushStream()
	if len(events) != 3 || events[2].Type != EventAssistantChunk || events[2].Text != "!" {
		t.Fatalf("events after flush = %#v, want flushed chunk delta", events)
	}
}

func TestUIStateAppendReasoningEmitsChunkDeltas(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	ui := NewUIState()
	var events []UIEvent
	ui.OnEvent = func(e UIEvent) {
		events = append(events, e)
	}

	ui.AppendReasoning("step 1")
	if len(events) != 1 || events[0].Type != EventReasoning || events[0].Text != "step 1" {
		t.Fatalf("events after first reasoning chunk = %#v, want immediate reasoning chunk", events)
	}

	ui.AppendReasoning(" + step 2")
	ui.FlushReasoning()
	if len(events) != 2 || events[1].Type != EventReasoning || events[1].Text != " + step 2" {
		t.Fatalf("events after flush = %#v, want flushed reasoning delta", events)
	}
}

func TestUIStateAppendAssistantTextFlushesPendingReasoning(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	ui := NewUIState()
	var events []UIEvent
	ui.OnEvent = func(e UIEvent) {
		events = append(events, e)
	}

	ui.AppendReasoning("first")
	ui.AppendReasoning(" second")
	ui.AppendAssistantText("answer")

	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
	if events[0].Type != EventReasoning || events[0].Text != "first" {
		t.Fatalf("first event = %#v, want immediate first reasoning chunk", events[0])
	}
	if events[1].Type != EventReasoning || events[1].Text != " second" {
		t.Fatalf("second event = %#v, want pending reasoning flushed before assistant text", events[1])
	}
	if events[2].Type != EventAppend || events[2].Msg.Role != "assistant" || events[2].Msg.Content != "answer" {
		t.Fatalf("third event = %#v, want assistant append after reasoning flush", events[2])
	}
}

func TestUIStateAppendChatFlushesPendingReasoningForNonUserMessages(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	ui := NewUIState()
	var events []UIEvent
	ui.OnEvent = func(e UIEvent) {
		events = append(events, e)
	}

	ui.AppendReasoning("first")
	ui.AppendReasoning(" second")
	ui.AppendChat(ChatMessage{Role: "tool", ToolName: "bash"})

	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
	if events[1].Type != EventReasoning || events[1].Text != " second" {
		t.Fatalf("second event = %#v, want pending reasoning flushed before non-user append", events[1])
	}
	if events[2].Type != EventAppend || events[2].Msg.Role != "tool" {
		t.Fatalf("third event = %#v, want tool append after reasoning flush", events[2])
	}
}

func TestAppendAgentEventsMergesConsecutiveTextChunks(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	st := &agentPanelState{
		toolWidgets: make(map[int]*toolWidgetRef),
		vbox:        container.NewVBox(),
	}
	panel := AgentPanelData{
		ID: "sa-1",
		Events: []AgentEventEntry{
			{Type: "text", Content: "Hello "},
			{Type: "text", Content: "world"},
		},
	}

	cv.appendAgentEvents(panel, st, 0)

	if len(st.vbox.Objects) != 1 {
		t.Fatalf("rendered object count = %d, want 1 merged markdown widget", len(st.vbox.Objects))
	}
	if st.textMD == nil {
		t.Fatal("textMD not tracked for active agent stream")
	}
	if got := st.textMD.Content(); got != "Hello world" {
		t.Fatalf("textMD content = %q, want %q", got, "Hello world")
	}
}

func TestAppendAgentEventsMergesConsecutiveReasoningChunks(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	st := &agentPanelState{
		toolWidgets: make(map[int]*toolWidgetRef),
		vbox:        container.NewVBox(),
	}
	panel := AgentPanelData{
		ID: "sa-2",
		Events: []AgentEventEntry{
			{Type: "reasoning", Content: "first"},
			{Type: "reasoning", Content: " second"},
		},
	}

	cv.appendAgentEvents(panel, st, 0)

	if len(st.vbox.Objects) != 1 {
		t.Fatalf("rendered object count = %d, want 1 merged reasoning accordion", len(st.vbox.Objects))
	}
	if st.reasoningMD == nil {
		t.Fatal("reasoningMD not tracked for active agent reasoning stream")
	}
	if got := st.reasoningMD.Content(); got != "first second" {
		t.Fatalf("reasoningMD content = %q, want %q", got, "first second")
	}
}

// TestAppendAgentEventsToolCallNoPreFillResult verifies that tool_call does not
// pre-fill the result content. The result should only appear when tool_result arrives.
func TestAppendAgentEventsToolCallNoPreFillResult(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	st := &agentPanelState{
		toolWidgets: make(map[int]*toolWidgetRef),
		vbox:        container.NewVBox(),
	}

	panel := AgentPanelData{
		Events: []AgentEventEntry{
			{Type: "tool_call", ToolID: "call_1", ToolName: "read_file", ToolArgs: `{"path":"/tmp/test.txt"}`},
			{Type: "tool_result", ToolID: "call_1", Content: "file contents here"},
		},
	}

	cv.appendAgentEvents(panel, st, 0)

	// The tool_call should have created a ref
	if len(st.toolWidgets) != 1 {
		t.Fatalf("expected 1 toolWidget, got %d", len(st.toolWidgets))
	}

	// The ref should have hasResult=true (set by tool_result)
	for _, ref := range st.toolWidgets {
		if !ref.hasResult {
			t.Error("expected hasResult=true after tool_result")
		}
	}
}

// TestAppendAgentEventsToolResultMatchesByToolID verifies that tool_result
// matches the correct tool_call by ToolID, not by event index.
func TestAppendAgentEventsToolResultMatchesByToolID(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	st := &agentPanelState{
		toolWidgets: make(map[int]*toolWidgetRef),
		vbox:        container.NewVBox(),
	}

	panel := AgentPanelData{
		Events: []AgentEventEntry{
			{Type: "tool_call", ToolID: "call_A", ToolName: "read_file", ToolArgs: `{"path":"A"}`},
			{Type: "text", Content: "thinking..."},
			{Type: "tool_call", ToolID: "call_B", ToolName: "run_command", ToolArgs: `{"command":"ls"}`},
			{Type: "tool_result", ToolID: "call_B", Content: "file1\nfile2"},
			{Type: "tool_result", ToolID: "call_A", Content: "content A"},
		},
	}

	cv.appendAgentEvents(panel, st, 0)

	if len(st.toolWidgets) != 2 {
		t.Fatalf("expected 2 toolWidgets, got %d", len(st.toolWidgets))
	}

	// Both should have hasResult=true
	foundA, foundB := false, false
	for _, ref := range st.toolWidgets {
		if !ref.hasResult {
			t.Errorf("tool %q has hasResult=false, expected true", ref.toolID)
		}
		if ref.toolID == "call_A" {
			foundA = true
		}
		if ref.toolID == "call_B" {
			foundB = true
		}
	}
	if !foundA {
		t.Error("call_A not found in toolWidgets")
	}
	if !foundB {
		t.Error("call_B not found in toolWidgets")
	}
}

// TestAppendAgentEventsNoDuplicateToolResult verifies that a tool_result event
// does not produce duplicate rendering when tool_call and tool_result arrive
// in the same batch.
func TestAppendAgentEventsNoDuplicateToolResult(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	st := &agentPanelState{
		toolWidgets: make(map[int]*toolWidgetRef),
		vbox:        container.NewVBox(),
	}

	panel := AgentPanelData{
		Events: []AgentEventEntry{
			{Type: "tool_call", ToolID: "call_1", ToolName: "read_file", ToolArgs: `{"path":"/tmp/test"}`},
			{Type: "tool_result", ToolID: "call_1", Content: "hello"},
		},
	}

	cv.appendAgentEvents(panel, st, 0)

	// Count result-related children in the vbox.
	// After fix, tool_call creates one block, tool_result adds one result block.
	// Before fix, tool_call pre-filled result AND tool_result added another = duplicate.
	// We check by counting: there should be exactly 1 tool block with hasResult=true.
	resultCount := 0
	for _, ref := range st.toolWidgets {
		if ref.hasResult {
			resultCount++
		}
	}
	if resultCount != 1 {
		t.Errorf("expected exactly 1 tool with hasResult=true, got %d", resultCount)
	}
}

// TestAppendAgentEventsToolResultIgnoresAlreadyMatched verifies that a second
// tool_result for the same ToolID is ignored (no duplicate addToolResult).
func TestAppendAgentEventsToolResultIgnoresAlreadyMatched(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	st := &agentPanelState{
		toolWidgets: make(map[int]*toolWidgetRef),
		vbox:        container.NewVBox(),
	}

	panel := AgentPanelData{
		Events: []AgentEventEntry{
			{Type: "tool_call", ToolID: "call_1", ToolName: "read_file", ToolArgs: `{"path":"/tmp/test"}`},
			{Type: "tool_result", ToolID: "call_1", Content: "first"},
			{Type: "tool_result", ToolID: "call_1", Content: "duplicate"},
		},
	}

	cv.appendAgentEvents(panel, st, 0)

	// The second tool_result should be ignored because hasResult is already true
	resultCount := 0
	for _, ref := range st.toolWidgets {
		if ref.hasResult {
			resultCount++
		}
	}
	if resultCount != 1 {
		t.Errorf("expected exactly 1 tool with hasResult=true (duplicate ignored), got %d", resultCount)
	}
}

func TestBuildToolRefFindsBodyAndAddsResultSectionWithTimelineRow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	msg := &ChatMessage{
		Role:     "tool",
		ToolName: "read_file",
		ToolDesc: "Read config",
		ToolID:   "tool-1",
	}

	w := cv.renderFileTool(msg)
	ref := cv.buildToolRef(msg, w)
	if ref == nil {
		t.Fatal("expected tool ref")
	}
	if ref.icon == nil {
		t.Fatal("expected tool icon to be found")
	}
	if ref.body == nil {
		t.Fatal("expected tool body container to be found")
	}

	cv.addToolResult(ref, "hello world", false)
	if len(ref.body.Objects) < 2 {
		t.Fatalf("expected body to contain header and result section, got %d objects", len(ref.body.Objects))
	}

	foundSection := false
	for _, child := range ref.body.Objects {
		if c, ok := child.(*fyne.Container); ok && len(c.Objects) == 2 {
			foundSection = true
		}
	}
	if !foundSection {
		t.Fatal("expected collapsible result section in tool body")
	}
}

func TestAddToolResultShowsStartCommandStatus(t *testing.T) {
	cv := &ChatView{}
	msg := &ChatMessage{
		Role:     "tool",
		ToolName: "start_command",
		ToolDesc: "Run in background",
		ToolID:   "tool-start",
	}

	w := cv.renderGenericTool(msg)
	ref := cv.buildToolRef(msg, w)
	if ref == nil || ref.body == nil {
		t.Fatal("expected tool body container")
	}

	cv.addToolResult(ref, "Job ID: cmd-1\nStatus: running\nDuration: 1s", false)
	if len(ref.body.Objects) < 2 {
		t.Fatalf("expected start_command result section, got %d objects", len(ref.body.Objects))
	}
}

func TestMessageRowWrapsContentInTimelineSurface(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{}
	row := cv.messageRow("AGENT", theme.ComputerIcon(), theme.ColorNamePrimary, widget.NewLabel("hello"))
	c, ok := row.(*fyne.Container)
	if !ok || len(c.Objects) == 0 {
		t.Fatalf("message row = %#v, want non-empty container", row)
	}
}

func TestRebuildFromMessagesUsesUserRendererForHistory(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	cv := &ChatView{
		vbox:  container.NewVBox(),
		entry: newSendEntry(),
	}
	cv.scroll = container.NewVScroll(cv.vbox)

	cv.rebuildFromMessages([]provider.Message{
		{
			Role: "user",
			Content: []provider.ContentBlock{
				provider.TextBlock("hello from history"),
			},
		},
	})

	if len(cv.msgWidgets) != 1 {
		t.Fatalf("msgWidgets = %d, want 1", len(cv.msgWidgets))
	}
	if !containsCanvasText(cv.msgWidgets[0], "USER") {
		t.Fatalf("expected rebuilt history message to contain USER tag, got %#v", cv.msgWidgets[0])
	}
	if containsCanvasText(cv.msgWidgets[0], "TOOL") {
		t.Fatalf("rebuilt user history should not contain TOOL tag, got %#v", cv.msgWidgets[0])
	}
}

func containsCanvasText(obj fyne.CanvasObject, want string) bool {
	switch v := obj.(type) {
	case *canvas.Text:
		return v.Text == want
	case *widget.Card:
		if v.Content != nil {
			return containsCanvasText(v.Content, want)
		}
	case *fyne.Container:
		for _, child := range v.Objects {
			if containsCanvasText(child, want) {
				return true
			}
		}
	}
	return false
}
