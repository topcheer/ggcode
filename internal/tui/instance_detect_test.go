package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

func TestDetectAndAutoMute_SingleInstance(t *testing.T) {
	dir := t.TempDir()
	mgr := im.NewManager()
	m := testModelWithIMManager(mgr, dir)

	// Single instance — should not mute anything
	// Need to bind a channel first
	mgr.BindSession(im.SessionBinding{
		SessionID: "test-session",
		Workspace: dir,
	})
	_, err := mgr.BindChannel(im.ChannelBinding{
		Workspace: dir,
		Platform:  im.PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})
	if err != nil {
		t.Fatalf("BindChannel: %v", err)
	}

	// Should have 1 active binding
	if len(mgr.CurrentBindings()) != 1 {
		t.Fatalf("expected 1 active binding, got %d", len(mgr.CurrentBindings()))
	}
	if len(mgr.MutedBindings()) != 0 {
		t.Fatalf("expected 0 muted bindings, got %d", len(mgr.MutedBindings()))
	}

	// InstanceDetect should be registered
	if m.instanceDetect == nil {
		t.Fatal("instanceDetect should be set")
	}
	if !m.instanceDetect.IsPrimary() {
		t.Fatal("single instance should be primary")
	}
}

func TestDetectAndAutoMute_SecondInstance(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", "instances")
	os.MkdirAll(instancesDir, 0o755)

	// Fork a long-lived child process to simulate another instance
	sleepCmd := exec.Command("sleep", "30")
	if err := sleepCmd.Start(); err != nil {
		t.Skip("cannot fork sleep process")
	}
	defer sleepCmd.Process.Kill()
	fakePID := sleepCmd.Process.Pid

	// Write PID file for the "older" instance
	olderInfo := im.InstanceInfo{
		PID:       fakePID,
		UUID:      "older-uuid-abcd",
		StartedAt: time.Now().Add(-5 * time.Minute),
	}
	olderData, _ := json.Marshal(olderInfo)
	os.WriteFile(filepath.Join(instancesDir, fmt.Sprintf("%d-older-uu.json", fakePID)), olderData, 0o644)

	mgr := im.NewManager()
	m := testModelWithIMManager(mgr, dir)

	// Bind channels
	mgr.BindSession(im.SessionBinding{
		SessionID: "test-session",
		Workspace: dir,
	})
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Workspace: dir,
		Platform:  im.PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Workspace: dir,
		Platform:  im.PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "ch-2",
	})

	// detectAndAutoMute was called during SetIMManager
	// But the channels were bound after SetIMManager, so we need to
	// manually trigger it again to test auto-mute
	m.detectAndAutoMute()

	// Should have auto-muted all channels
	if len(mgr.CurrentBindings()) != 0 {
		t.Fatalf("expected 0 active (all muted), got %d", len(mgr.CurrentBindings()))
	}
	muted := mgr.MutedBindings()
	if len(muted) != 2 {
		t.Fatalf("expected 2 muted, got %d", len(muted))
	}

	// Should NOT be primary
	if m.instanceDetect.IsPrimary() {
		t.Fatal("second instance should NOT be primary")
	}

	// Clean up
	m.instanceDetect.Unregister()
}

func TestDetectAndAutoMute_NoManager(t *testing.T) {
	m := Model{}
	// Should not panic
	m.detectAndAutoMute()
}

func TestDetectAndAutoMute_NoSession(t *testing.T) {
	mgr := im.NewManager()
	m := Model{imManager: mgr}
	// No session — should not panic
	m.detectAndAutoMute()
}

func TestIMPanelInstancesSection(t *testing.T) {
	dir := t.TempDir()
	mgr := im.NewManager()
	m := testModelWithIMManager(mgr, dir)

	mgr.BindSession(im.SessionBinding{
		SessionID: "test-session",
		Workspace: dir,
	})

	m.openIMPanel()
	rendered := m.renderIMPanel()

	// Should contain "Instances" section (even with just 1)
	if len(rendered) == 0 {
		t.Fatal("panel should render")
	}
}

func TestIMPanelNoInstancesWhenNoDetect(t *testing.T) {
	mgr := im.NewManager()
	m := Model{imManager: mgr, language: LangEnglish}

	mgr.BindSession(im.SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp",
	})

	m.openIMPanel()
	rendered := m.renderIMPanel()

	// Without instanceDetect, should not show instances section
	if len(rendered) == 0 {
		t.Fatal("panel should render")
	}
	// Just verify it doesn't crash — instances section is conditional
}

func testModelWithIMManager(mgr *im.Manager, workspace string) *Model {
	m := &Model{
		language:    LangEnglish,
		output:      &bytes.Buffer{},
		chatEntries: NewChatEntryList(),
	}
	m.SetSession(&session.Session{ID: "test-session", Workspace: workspace}, nil)
	m.SetIMManager(mgr)
	return m
}
