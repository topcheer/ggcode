package main

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
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
