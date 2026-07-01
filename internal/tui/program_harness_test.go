package tui

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

type harnessBarrierMsg struct {
	done chan struct{}
}

type harnessSnapshotMsg struct {
	reply chan Model
}

type liveProgramModel struct {
	inner Model
}

func (m liveProgramModel) Init() tea.Cmd {
	return m.inner.Init()
}

func (m liveProgramModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case harnessBarrierMsg:
		close(typed.done)
		return m, nil
	case harnessSnapshotMsg:
		typed.reply <- m.inner
		return m, nil
	}

	next, cmd := m.inner.Update(msg)
	switch typed := next.(type) {
	case Model:
		m.inner = typed
	case *Model:
		m.inner = *typed
	default:
		panic("unexpected model type")
	}
	return m, cmd
}

func (m liveProgramModel) View() tea.View {
	return m.inner.View()
}

type liveProgramResult struct {
	model tea.Model
	err   error
}

type liveProgramHarness struct {
	t       *testing.T
	program *tea.Program
	input   *io.PipeWriter
	done    chan liveProgramResult
}

func startLiveProgramHarness(t *testing.T, model Model) *liveProgramHarness {
	t.Helper()

	inputReader, inputWriter := io.Pipe()
	program := tea.NewProgram(
		liveProgramModel{inner: model},
		tea.WithInput(inputReader),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignals(),
	)
	h := &liveProgramHarness{
		t:       t,
		program: program,
		input:   inputWriter,
		done:    make(chan liveProgramResult, 1),
	}
	go func() {
		finalModel, err := program.Run()
		h.done <- liveProgramResult{model: finalModel, err: err}
	}()

	time.Sleep(25 * time.Millisecond)
	h.program.Send(setProgramMsg{Program: h.program})
	h.program.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.program.Send(tea.KeyboardEnhancementsMsg{Flags: 1})
	h.sync()
	// Wait for the startup input drain to end (setProgramMsg triggers a
	// 250ms tea.Tick that sends inputDrainEndMsg). All tests that send
	// keyboard input need the drain to have completed first.
	time.Sleep(300 * time.Millisecond)
	h.sync()
	return h
}

func (h *liveProgramHarness) send(msg tea.Msg) {
	h.t.Helper()
	h.program.Send(msg)
}

func (h *liveProgramHarness) sync() {
	h.t.Helper()
	done := make(chan struct{})
	h.program.Send(harnessBarrierMsg{done: done})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		h.t.Fatal("timed out waiting for program to process barrier")
	}
}

func (h *liveProgramHarness) snapshot() Model {
	h.t.Helper()
	reply := make(chan Model, 1)
	h.program.Send(harnessSnapshotMsg{reply: reply})
	select {
	case state := <-reply:
		return state
	case <-time.After(2 * time.Second):
		h.t.Fatal("timed out waiting for program snapshot")
		return Model{}
	}
}

func (h *liveProgramHarness) close() Model {
	h.t.Helper()
	h.program.Quit()
	_ = h.input.Close()
	select {
	case result := <-h.done:
		if result.err != nil && !errors.Is(result.err, tea.ErrInterrupted) {
			h.t.Fatalf("program exited with error: %v", result.err)
		}
		switch typed := result.model.(type) {
		case liveProgramModel:
			return typed.inner
		case *liveProgramModel:
			return typed.inner
		default:
			h.t.Fatalf("unexpected final model type %T", result.model)
			return Model{}
		}
	case <-time.After(2 * time.Second):
		h.t.Fatal("timed out waiting for program to exit")
		return Model{}
	}
}

func waitForProgramState(t *testing.T, h *liveProgramHarness, predicate func(Model) bool) Model {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last Model
	for time.Now().Before(deadline) {
		last = h.snapshot()
		if predicate(last) {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for expected program state")
	return last
}

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

func TestTruncateHarnessTextKeepsUTF8Valid(t *testing.T) {
	got := truncateHarnessText("定位 TUI 面板和主输入框粘贴处理中文截断问题", 8)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8, got %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("expected no replacement rune, got %q", got)
	}
}
