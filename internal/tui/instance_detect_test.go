package tui

import (
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
	mgr.SetBindingStore(&stubBindingStore{
		bindings: []im.ChannelBinding{
			{Workspace: dir, Platform: im.PlatformQQ, Adapter: "qq-bot-1", ChannelID: "ch-1"},
		},
	})

	m := newTestModel()
	m.SetIMManager(mgr)
	m.SetSession(&session.Session{ID: "test-session", Workspace: dir}, nil)

	if len(mgr.CurrentBindings()) != 1 {
		t.Fatalf("expected 1 active binding, got %d", len(mgr.CurrentBindings()))
	}
	if len(mgr.MutedBindings()) != 0 {
		t.Fatalf("expected 0 muted bindings, got %d", len(mgr.MutedBindings()))
	}

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

	sleepCmd := exec.Command("sleep", "30")
	if err := sleepCmd.Start(); err != nil {
		t.Skip("cannot fork sleep process")
	}
	defer sleepCmd.Process.Kill()
	fakePID := sleepCmd.Process.Pid

	olderInfo := im.InstanceInfo{
		PID:       fakePID,
		UUID:      "older-uuid-abcd",
		StartedAt: time.Now().Add(-5 * time.Minute),
	}
	olderData, _ := json.Marshal(olderInfo)
	os.WriteFile(filepath.Join(instancesDir, fmt.Sprintf("%d-older-uu.json", fakePID)), olderData, 0o644)

	mgr := im.NewManager()

	mgr.SetBindingStore(&stubBindingStore{
		bindings: []im.ChannelBinding{
			{Workspace: dir, Platform: im.PlatformQQ, Adapter: "qq-bot-1", ChannelID: "ch-1", LastSessionID: "other-session"},
			{Workspace: dir, Platform: im.PlatformTelegram, Adapter: "tg-bot-1", ChannelID: "ch-2", LastSessionID: "other-session"},
		},
	})

	m := newTestModel()
	m.SetIMManager(mgr)
	m.SetSession(&session.Session{ID: "test-session", Workspace: dir}, nil)

	all := mgr.CurrentBindings()
	if len(all) != 2 {
		t.Fatalf("expected 2 bindings loaded, got %d", len(all))
	}
	for _, b := range all {
		if !b.Muted {
			t.Fatalf("expected binding %s to be muted (owned by other session)", b.Adapter)
		}
	}
	muted := mgr.MutedBindings()
	if len(muted) != 2 {
		t.Fatalf("expected 2 muted, got %d", len(muted))
	}

	if m.instanceDetect.IsPrimary() {
		t.Fatal("second instance should NOT be primary")
	}

	m.instanceDetect.Unregister()
}

func TestDetectAndAutoMute_NoManager(t *testing.T) {
	m := Model{}

	m.detectAndAutoMute()
}

func TestDetectAndAutoMute_NoSession(t *testing.T) {
	mgr := im.NewManager()
	m := Model{imManager: mgr}

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

	if len(rendered) == 0 {
		t.Fatal("panel should render")
	}

}

func testModelWithIMManager(mgr *im.Manager, workspace string) *Model {
	m := newTestModel()
	m.SetSession(&session.Session{ID: "test-session", Workspace: workspace}, nil)
	m.SetIMManager(mgr)
	return &m
}

type stubBindingStore struct {
	bindings []im.ChannelBinding
}

func (s *stubBindingStore) Save(b im.ChannelBinding) error {
	for i, existing := range s.bindings {
		if existing.Workspace == b.Workspace && existing.Adapter == b.Adapter {
			s.bindings[i] = b
			return nil
		}
	}
	s.bindings = append(s.bindings, b)
	return nil
}
func (s *stubBindingStore) Delete(workspace, adapter string) error {
	for i, b := range s.bindings {
		if b.Workspace == workspace && b.Adapter == adapter {
			s.bindings = append(s.bindings[:i], s.bindings[i+1:]...)
			return nil
		}
	}
	return nil
}
func (s *stubBindingStore) List() ([]im.ChannelBinding, error) {
	return append([]im.ChannelBinding{}, s.bindings...), nil
}
func (s *stubBindingStore) ListByWorkspace(ws string) ([]im.ChannelBinding, error) {
	var result []im.ChannelBinding
	for _, b := range s.bindings {
		if b.Workspace == ws {
			result = append(result, b)
		}
	}
	return result, nil
}
func (s *stubBindingStore) ListByAdapter(adapter string) ([]im.ChannelBinding, error) {
	var result []im.ChannelBinding
	for _, b := range s.bindings {
		if b.Adapter == adapter {
			result = append(result, b)
		}
	}
	return result, nil
}
func (s *stubBindingStore) BindExclusive(b im.ChannelBinding) error {
	filtered := s.bindings[:0]
	for _, existing := range s.bindings {
		if existing.Adapter != b.Adapter {
			filtered = append(filtered, existing)
		}
	}
	s.bindings = append(filtered, b)
	return nil
}
func (s *stubBindingStore) UpdateSessionID(workspace, adapter, sessionID string) error {
	for i, b := range s.bindings {
		if b.Workspace == workspace && b.Adapter == adapter {
			s.bindings[i].LastSessionID = sessionID
			return nil
		}
	}
	return nil
}
