package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/safego"
)

// Anti-rug-pull tool baselines (research sa-119, MCPShield TC2 TV5/TV6):
// an MCP server's tool definitions are approved once by the user, and the
// content hash of that approval is persisted across restarts. Without
// persistence the in-memory toolsHash forgets everything on restart, so a
// server that swaps a tool description (or rolls a version back) after the
// initial approval was completely invisible.

// baselineFileVersion reserves room for format evolution.
const baselineFileVersion = 1

// driftOnboardingWindow is the post-approval window during which tool
// definition churn is treated as expected (first-day iteration, hot
// development servers) rather than a rug pull. Changes inside the window
// still update the baseline; they just do not raise a drift alert.
const driftOnboardingWindow = 24 * time.Hour

// ToolBaseline is the persisted, user-approved tool definition snapshot
// for one MCP server.
type ToolBaseline struct {
	ToolsHash  string    `json:"tools_hash"`
	ApprovedAt time.Time `json:"approved_at"`
	ToolNames  []string  `json:"tool_names,omitempty"`
}

type baselineFile struct {
	Version int                      `json:"version"`
	Servers map[string]*ToolBaseline `json:"servers"`
}

// ToolBaselineStore persists per-server tool baselines to
// <configDir>/mcp_baselines.json. All read-modify-write cycles are
// serialized by mu; writes are atomic (tmp file + os.Rename) and never
// panic - a failed write is returned as an error so callers can degrade
// to the debug log.
type ToolBaselineStore struct {
	mu  sync.Mutex
	dir string // empty resolves to config.ConfigDir() lazily
}

// NewToolBaselineStore creates a store rooted at dir. An empty dir uses
// the default user config directory (~/.ggcode), resolved at I/O time so
// construction stays side-effect free (tests can also point it anywhere).
func NewToolBaselineStore(dir string) *ToolBaselineStore {
	return &ToolBaselineStore{dir: dir}
}

func (s *ToolBaselineStore) path() string {
	dir := s.dir
	if dir == "" {
		dir = config.ConfigDir()
	}
	return filepath.Join(dir, "mcp_baselines.json")
}

// load reads the whole baseline file under the store lock. A missing file
// is an empty map, not an error.
func (s *ToolBaselineStore) load() (map[string]*ToolBaseline, error) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*ToolBaseline{}, nil
		}
		return nil, err
	}
	var file baselineFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse mcp_baselines.json: %w", err)
	}
	if file.Servers == nil {
		file.Servers = map[string]*ToolBaseline{}
	}
	return file.Servers, nil
}

// saveLocked persists the servers map atomically. Caller holds s.mu.
func (s *ToolBaselineStore) saveLocked(servers map[string]*ToolBaseline) error {
	file := baselineFile{Version: baselineFileVersion, Servers: servers}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	dir := s.dir
	if dir == "" {
		dir = config.ConfigDir()
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return mkErr
	}
	path := filepath.Join(dir, "mcp_baselines.json")
	tmp, err := os.CreateTemp(dir, ".mcp_baselines-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// LoadBaseline returns the persisted baseline for name, or (nil, nil) when
// the server has no baseline yet (first connection).
func (s *ToolBaselineStore) LoadBaseline(name string) (*ToolBaseline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	servers, err := s.load()
	if err != nil {
		return nil, err
	}
	base := servers[name]
	if base == nil {
		return nil, nil
	}
	copy_ := *base
	return &copy_, nil
}

// SaveBaseline records the approved tool snapshot for name. ApprovedAt is
// stamped now: the baseline moves forward with every observed change, so a
// drift alert fires once per change instead of replaying forever.
func (s *ToolBaselineStore) SaveBaseline(name, hash string, names []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	servers, err := s.load()
	if err != nil {
		return err
	}
	servers[name] = &ToolBaseline{
		ToolsHash:  hash,
		ApprovedAt: time.Now(),
		ToolNames:  sortedCopy(names),
	}
	return s.saveLocked(servers)
}

// CompareAndReport diffs the persisted baseline for name against the
// freshly observed tool set. A matching hash short-circuits to
// drifted=false; a mismatch reports the name-level added/removed sets
// (definition-only edits surface as an empty diff but drifted=true).
func (s *ToolBaselineStore) CompareAndReport(name, newHash string, newNames []string) (added, removed []string, drifted bool) {
	base, err := s.LoadBaseline(name)
	if err != nil || base == nil {
		return nil, nil, false
	}
	if base.ToolsHash == newHash {
		return nil, nil, false
	}
	added, removed, drifted = diffToolNames(newNames, base.ToolNames), diffToolNames(base.ToolNames, newNames), true
	return
}

// diffToolNames returns the sorted names present in a but not in b.
func diffToolNames(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, name := range b {
		set[name] = struct{}{}
	}
	var out []string
	for _, name := range a {
		if _, ok := set[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// toolNamesOf extracts the tool names for baseline diffing.
func toolNamesOf(tools []mcp.ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}

func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// driftInOnboardingWindow reports whether a change observed at now falls
// inside the 24h post-approval onboarding window for a baseline approved
// at approvedAt. A zero ApprovedAt (corrupt legacy record) is treated as
// outside the window so drift still surfaces.
func driftInOnboardingWindow(approvedAt, now time.Time) bool {
	if approvedAt.IsZero() {
		return false
	}
	return now.Sub(approvedAt) < driftOnboardingWindow
}

// recordToolBaselineLocked applies the anti-rug-pull policy for one
// observed tool set. It must be called with m.mu held (same lock domain
// as the m.toolsHash update that precedes it). It returns the drift diff
// and whether the OnToolDrift callback should fire:
//
//   - no store attached: no-op (unit fixtures without persistence);
//   - no baseline yet: first connection IS the approval - save silently;
//   - hash match: nothing changed, short-circuit;
//   - hash mismatch: always move the baseline forward (one alert per
//     change, never a replay), but only alert when the change escapes
//     the false-positive guards (AllowToolDrift escape hatch, 24h
//     onboarding window).
//
// I/O failures degrade to a debug log - persistence is a guardrail, not a
// hard dependency of connecting.
func (m *MCPPlugin) recordToolBaselineLocked(newHash string, newNames []string) (added, removed []string, fire bool) {
	store := m.baselines
	if store == nil {
		return nil, nil, false
	}
	base, err := store.LoadBaseline(m.cfg.Name)
	if err != nil {
		debug.Log("mcp-drift", "server=%s baseline load failed (continuing without drift check): %v", m.cfg.Name, err)
		return nil, nil, false
	}
	if base == nil {
		// First observed connection: this tool set is what the user is
		// approving by enabling the server. Persist it as the baseline.
		if err := store.SaveBaseline(m.cfg.Name, newHash, newNames); err != nil {
			debug.Log("mcp-drift", "server=%s baseline save failed: %v", m.cfg.Name, err)
		}
		return nil, nil, false
	}
	if base.ToolsHash == newHash {
		return nil, nil, false
	}
	added, removed, _ = store.CompareAndReport(m.cfg.Name, newHash, newNames)
	if err := store.SaveBaseline(m.cfg.Name, newHash, newNames); err != nil {
		debug.Log("mcp-drift", "server=%s baseline save failed: %v", m.cfg.Name, err)
	}
	if m.cfg.AllowToolDrift {
		debug.Log("mcp-drift", "server=%s tool definitions changed but allow_tool_drift is set: %+v / -%+v", m.cfg.Name, added, removed)
		return nil, nil, false
	}
	if driftInOnboardingWindow(base.ApprovedAt, time.Now()) {
		debug.Log("mcp-drift", "server=%s tool definitions changed inside 24h onboarding window (baseline updated silently)", m.cfg.Name)
		return nil, nil, false
	}
	debug.Log("mcp-drift", "server=%s TOOL DRIFT after approval: added=%v removed=%v", m.cfg.Name, added, removed)
	return added, removed, true
}

// notifyToolDrift fires the drift callback off the plugin lock. Connect
// runs under a deferred m.mu.Unlock, so the callback is dispatched via
// safego.Go to avoid holding the plugin lock across user code (the
// callback may snapshot MCP state, which takes the same lock's read side).
func (m *MCPPlugin) notifyToolDrift(added, removed []string) {
	cb := m.OnToolDrift
	if cb == nil {
		return
	}
	server := m.cfg.Name
	safego.Go("plugin.mcp.drift", func() { cb(server, added, removed) })
}
