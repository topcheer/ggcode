//go:build integration

package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestLivePromptEmitsSingleFinalIMText verifies that a live bubbletea program
// emits a single merged IM text event for the assistant reply after a user
// submits a prompt. This is an integration test because it runs a real
// bubbletea event loop with goroutine scheduling and timing dependencies.
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
	imMgr.RegisterSink(sink) // re-register after detectAndAutoMute
	m.input.SetValue("ping")

	h := startLiveProgramHarness(t, m)
	defer h.close()
	h.send(tea.KeyPressMsg{Code: tea.KeyEnter})

	waitForProgramState(t, h, func(state Model) bool {
		return !state.loading
	})

	deadline := time.Now().Add(5 * time.Second)
	var events []im.OutboundEvent
	for time.Now().Before(deadline) {
		events = sink.snapshot()
		if len(events) >= 2 {
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

// TestLiveProgramHarnessProcessesKeyEventsAndPersistsMode verifies that a live
// bubbletea program correctly processes keyboard input and mode switching.
// Integration test: depends on real event loop scheduling.
func TestLiveProgramHarnessProcessesKeyEventsAndPersistsMode(t *testing.T) {
	m := newTestModel()
	// Mode is now persisted to session metadata, not config file.
	ses := session.NewSession("", "", "")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "init"}}}}
	m.session = ses

	h := startLiveProgramHarness(t, m)
	defer h.close()

	h.send(tea.KeyPressMsg{Text: "h"})
	h.send(tea.KeyPressMsg{Text: "i"})
	h.send(tea.KeyPressMsg{Text: "shift+tab"})
	h.sync()

	state := h.snapshot()
	if got := state.input.Value(); got != "hi" {
		t.Fatalf("expected live program input %q, got %q", "hi", got)
	}
	if state.mode != permission.PlanMode {
		t.Fatalf("expected live program mode %v, got %v", permission.PlanMode, state.mode)
	}
	if ses.PermissionMode != permission.PlanMode.String() {
		t.Fatalf("expected session.PermissionMode %q, got %q", permission.PlanMode.String(), ses.PermissionMode)
	}
}

// TestLiveProgramHarnessExecutesAsyncClipboardPasteCommand verifies clipboard
// paste via ctrl+v in a live program. Integration test: depends on async
// command execution and goroutine scheduling.
func TestLiveProgramHarnessExecutesAsyncClipboardPasteCommand(t *testing.T) {
	m := newTestModel()
	m.clipboardLoader = func() (imageAttachedMsg, error) {
		img := image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10}
		return imageAttachedMsg{
			placeholder: image.Placeholder("ggcode-image-deadbeef.png", img),
			img:         img,
			filename:    "ggcode-image-deadbeef.png",
			sourcePath:  "/tmp/ggcode-image-deadbeef.png",
		}, nil
	}

	h := startLiveProgramHarness(t, m)
	defer h.close()

	h.send(tea.KeyPressMsg{Text: "ctrl+v"})

	state := waitForProgramState(t, h, func(state Model) bool {
		return len(state.pendingImages) > 0
	})
	if len(state.pendingImages) == 0 {
		t.Fatal("expected live program clipboard paste to attach an image")
	}
	if state.pendingImages[0].sourcePath != "/tmp/ggcode-image-deadbeef.png" {
		t.Fatalf("expected source path to survive async command, got %q", state.pendingImages[0].sourcePath)
	}
}
