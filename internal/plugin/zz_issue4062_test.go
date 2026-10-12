package plugin

// #4062 companion tests: the sa-119 anti-rug-pull alert had only its
// detection half alive in production - OnToolDrift was nil on every
// production wiring path (only tests assigned it), so a tampered MCP
// server was detected, silently re-baselined, and never reported.
// These tests pin (a) the manager wires the sink on BOTH construction
// paths, (b) the sink actually surfaces a warning, and (c) a nil sink
// keeps the approved baseline instead of whitewashing the tamper.

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Both production assembly paths must hand every plugin a non-nil drift
// sink: NewMCPManager (initial load) and newPluginFromConfig (reload).
func TestIssue4062_ManagerWiresDriftSink(t *testing.T) {
	servers := []config.MCPServerConfig{{Name: "a"}, {Name: "b"}}
	m := NewMCPManager(servers, nil, "scope")
	for _, p := range m.plugins {
		if p.OnToolDrift == nil {
			t.Fatalf("plugin %s built by NewMCPManager has nil OnToolDrift - drift alert would die unwired (#4062)", p.Name())
		}
	}
	// Reload path.
	p := m.newPluginFromConfig(config.MCPServerConfig{Name: "c"})
	if p.OnToolDrift == nil {
		t.Fatal("plugin built by newPluginFromConfig has nil OnToolDrift - reload path drops the drift sink (#4062)")
	}
}

// The sink must record a user-visible warning on the manager surface.
func TestIssue4062_HandleToolDriftRecordsWarning(t *testing.T) {
	m := NewMCPManager(nil, nil, "scope")
	m.handleToolDrift("srv", []string{"evil_tool"}, []string{"old"})
	m.mu.RLock()
	warnings := append([]string(nil), m.warnings...)
	m.mu.RUnlock()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1 drift warning", warnings)
	}
	if !strings.Contains(warnings[0], "srv") || !strings.Contains(warnings[0], "evil_tool") {
		t.Fatalf("warning must name the server and the added tool: %q", warnings[0])
	}
}

// ageBaselineBeyondWindow rewrites the baseline's approval timestamps to
// 48h ago so drift escapes the onboarding window (mirrors the fixture in
// TestRecordToolBaseline_DriftAfterApproval).
func ageBaselineBeyondWindow(t *testing.T, m *MCPPlugin) {
	t.Helper()
	m.baselines.mu.Lock()
	defer m.baselines.mu.Unlock()
	servers, err := m.baselines.load()
	if err != nil {
		t.Fatal(err)
	}
	servers[m.cfg.Name].ApprovedAt = time.Now().Add(-48 * time.Hour)
	servers[m.cfg.Name].FirstApprovedAt = time.Now().Add(-48 * time.Hour)
	if err := m.baselines.saveLocked(servers); err != nil {
		t.Fatal(err)
	}
}

// Fail-visible hardening: with NO sink wired, post-window drift must keep
// the approved baseline (the tampered hash must not be whitewashed), so
// the drift re-detects on the next observation.
func TestIssue4062_NilSinkKeepsBaselineFailVisible(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1", "t2"})
	m.mu.Unlock()
	ageBaselineBeyondWindow(t, m)

	m.mu.Lock()
	added, removed, fire := m.recordToolBaselineLocked("h2", []string{"t1", "t3"})
	m.mu.Unlock()
	if !fire {
		t.Fatal("post-window drift must still fire")
	}
	if len(added) != 1 || added[0] != "t3" || len(removed) != 1 || removed[0] != "t2" {
		t.Fatalf("diff: added=%v removed=%v", added, removed)
	}

	b, err := m.baselines.LoadBaseline("srv")
	if err != nil || b == nil {
		t.Fatalf("baseline load: (%v, %v)", b, err)
	}
	if b.ToolsHash != "h1" {
		t.Fatalf("nil sink must NOT re-baseline: hash=%q, want approved %q (#4062 fail-visible)", b.ToolsHash, "h1")
	}
	// Re-detection: the same tampered observation must fire AGAIN (the
	// tamper trace is not erased).
	m.mu.Lock()
	_, _, fire = m.recordToolBaselineLocked("h2", []string{"t1", "t3"})
	m.mu.Unlock()
	if !fire {
		t.Fatal("with nil sink the tampered tool set must re-detect on re-observation - it was silently whitewashed instead (#4062)")
	}
}
