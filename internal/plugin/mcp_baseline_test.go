package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestMain(m *testing.M) {

	if os.Getenv("GGCODE_TEST_ALLOW_REAL_HOME") == "" {
		dir, err := os.MkdirTemp("", "plugin-test-home-*")
		if err == nil {
			_ = os.Setenv("HOME", dir)
			defer os.RemoveAll(dir)
		}
	}
	os.Exit(m.Run())
}

func TestToolBaselineStore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	s := NewToolBaselineStore(dir)

	if b, err := s.LoadBaseline("srv"); err != nil || b != nil {
		t.Fatalf("missing baseline: got (%v, %v), want (nil, nil)", b, err)
	}
	if err := s.SaveBaseline("srv", "hash1", []string{"b", "a"}); err != nil {
		t.Fatal(err)
	}

	s2 := NewToolBaselineStore(dir)
	b, err := s2.LoadBaseline("srv")
	if err != nil || b == nil {
		t.Fatalf("reload: got (%v, %v)", b, err)
	}
	if b.ToolsHash != "hash1" {
		t.Fatalf("hash: got %q, want hash1", b.ToolsHash)
	}

	if len(b.ToolNames) != 2 || b.ToolNames[0] != "a" || b.ToolNames[1] != "b" {
		t.Fatalf("names: got %v, want [a b]", b.ToolNames)
	}
}

func TestToolBaselineStore_CompareAndReport(t *testing.T) {
	dir := t.TempDir()
	s := NewToolBaselineStore(dir)
	if err := s.SaveBaseline("srv", "hash1", []string{"keep", "gone"}); err != nil {
		t.Fatal(err)
	}

	added, removed, drifted := s.CompareAndReport("srv", "hash1", []string{"keep", "gone"})
	if drifted || added != nil || removed != nil {
		t.Fatalf("same hash: got (%v, %v, %v), want no drift", added, removed, drifted)
	}

	s2 := NewToolBaselineStore(dir)
	added, removed, drifted = s2.CompareAndReport("srv", "hash2", []string{"keep", "new"})
	if !drifted {
		t.Fatal("changed hash must drift")
	}
	if len(added) != 1 || added[0] != "new" {
		t.Fatalf("added: got %v, want [new]", added)
	}
	if len(removed) != 1 || removed[0] != "gone" {
		t.Fatalf("removed: %v, want [gone]", removed)
	}
}

func TestToolBaselineStore_SaveFailsGracefully(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	s := NewToolBaselineStore(filepath.Join(ro, "nested"))

	if err := s.SaveBaseline("srv", "h", nil); err == nil {
		t.Log("save unexpectedly succeeded (filesystem may allow the write)")
	}
	if _, err := s.LoadBaseline("srv"); err != nil {
		t.Fatalf("load after failed save: %v", err)
	}
}

func newBaselineTestPlugin(t *testing.T, cfg config.MCPServerConfig) *MCPPlugin {
	t.Helper()
	return &MCPPlugin{
		cfg:       cfg,
		status:    MCPStatusPending,
		baselines: NewToolBaselineStore(t.TempDir()),
	}
}

func TestRecordToolBaseline_FirstConnectionIsTheBaseline(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	added, removed, fire := m.recordToolBaselineLocked("h1", []string{"t1"})
	m.mu.Unlock()
	if added != nil || removed != nil || fire {
		t.Fatalf("first connection: got (%v,%v,%v), want silent baseline save", added, removed, fire)
	}
	if b, err := m.baselines.LoadBaseline("srv"); err != nil || b == nil || b.ToolsHash != "h1" {
		t.Fatalf("baseline not persisted: (%v, %v)", b, err)
	}
}

func TestRecordToolBaseline_DriftAfterApproval(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})

	m.OnToolDrift = func(server string, added, removed []string) {}
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1", "t2"})
	m.mu.Unlock()

	m.baselines.mu.Lock()
	servers, err := m.baselines.load()
	if err != nil {
		m.baselines.mu.Unlock()
		t.Fatal(err)
	}
	servers["srv"].ApprovedAt = time.Now().Add(-48 * time.Hour)
	servers["srv"].FirstApprovedAt = time.Now().Add(-48 * time.Hour)
	if err := m.baselines.saveLocked(servers); err != nil {
		m.baselines.mu.Unlock()
		t.Fatal(err)
	}
	m.baselines.mu.Unlock()

	m.mu.Lock()
	added, removed, fire := m.recordToolBaselineLocked("h2", []string{"t1", "t3"})
	m.mu.Unlock()
	if !fire {
		t.Fatal("post-window drift must fire")
	}
	if len(added) != 1 || added[0] != "t3" || len(removed) != 1 || removed[0] != "t2" {
		t.Fatalf("diff: added=%v removed=%v", added, removed)
	}

	m.mu.Lock()
	_, _, fire = m.recordToolBaselineLocked("h2", []string{"t1", "t3"})
	m.mu.Unlock()
	if fire {
		t.Fatal("baseline moved forward; replay must not re-fire")
	}
}

func TestRecordToolBaseline_OnboardingWindowSuppressesAlert(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})

	added, removed, fire := m.recordToolBaselineLocked("h2", []string{"t1"})
	m.mu.Unlock()
	if fire || added != nil || removed != nil {
		t.Fatalf("onboarding window: got (%v,%v,%v), want silent", added, removed, fire)
	}
	if b, _ := m.baselines.LoadBaseline("srv"); b == nil || b.ToolsHash != "h2" {
		t.Fatalf("window change must still move baseline forward: %v", b)
	}
}

func TestRecordToolBaseline_AllowToolDriftEscapeHatch(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv", AllowToolDrift: true})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})

	servers, err := m.baselines.load()
	if err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	servers["srv"].ApprovedAt = time.Now().Add(-48 * time.Hour)
	servers["srv"].FirstApprovedAt = time.Now().Add(-48 * time.Hour)
	m.baselines.saveLocked(servers)
	_, _, fire := m.recordToolBaselineLocked("h2", []string{"t1"})
	m.mu.Unlock()
	if fire {
		t.Fatal("allow_tool_drift must suppress the alert")
	}
}

func TestRecordToolBaseline_NilStoreIsNoop(t *testing.T) {
	m := &MCPPlugin{cfg: config.MCPServerConfig{Name: "srv"}}
	m.mu.Lock()
	added, removed, fire := m.recordToolBaselineLocked("h1", nil)
	m.mu.Unlock()
	if added != nil || removed != nil || fire {
		t.Fatal("nil store must be a no-op (unit fixtures)")
	}
}

func TestNotifyToolDrift_AsyncCallback(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	got := make(chan []string, 1)
	m.OnToolDrift = func(server string, added, removed []string) {
		got <- []string{server, added[0], removed[0]}
	}

	m.mu.Lock()
	m.notifyToolDrift([]string{"new"}, []string{"old"})
	m.mu.Unlock()
	select {
	case v := <-got:
		if v[0] != "srv" || v[1] != "new" || v[2] != "old" {
			t.Fatalf("callback args: %v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drift callback never fired")
	}

	m.OnToolDrift = nil
	m.notifyToolDrift(nil, nil)
}

func TestToolNamesOf(t *testing.T) {
	got := toolNamesOf(nil)
	if len(got) != 0 {
		t.Fatalf("nil tools: %v", got)
	}
}
