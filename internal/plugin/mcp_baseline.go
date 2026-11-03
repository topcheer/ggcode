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

const baselineFileVersion = 1

const driftOnboardingWindow = 24 * time.Hour

type ToolBaseline struct {
	ToolsHash string `json:"tools_hash"`

	ApprovedAt time.Time `json:"approved_at"`

	FirstApprovedAt time.Time `json:"first_approved_at,omitempty"`
	ToolNames       []string  `json:"tool_names,omitempty"`
}

type baselineFile struct {
	Version int                      `json:"version"`
	Servers map[string]*ToolBaseline `json:"servers"`
}

type ToolBaselineStore struct {
	mu  sync.Mutex
	dir string
}

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

func (s *ToolBaselineStore) SaveBaseline(name, hash string, names []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	servers, err := s.load()
	if err != nil {
		return err
	}
	now := time.Now()
	first := now
	if prev, ok := servers[name]; ok && !prev.FirstApprovedAt.IsZero() {
		first = prev.FirstApprovedAt
	}
	servers[name] = &ToolBaseline{
		ToolsHash:       hash,
		ApprovedAt:      now,
		FirstApprovedAt: first,
		ToolNames:       sortedCopy(names),
	}
	return s.saveLocked(servers)
}

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

func driftInOnboardingWindow(approvedAt, now time.Time) bool {
	if approvedAt.IsZero() {
		return false
	}
	return now.Sub(approvedAt) < driftOnboardingWindow
}

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

		if err := store.SaveBaseline(m.cfg.Name, newHash, newNames); err != nil {
			debug.Log("mcp-drift", "server=%s baseline save failed: %v", m.cfg.Name, err)
		}
		return nil, nil, false
	}
	if base.ToolsHash == newHash {
		return nil, nil, false
	}
	added, removed, _ = store.CompareAndReport(m.cfg.Name, newHash, newNames)
	if m.cfg.AllowToolDrift {
		if err := store.SaveBaseline(m.cfg.Name, newHash, newNames); err != nil {
			debug.Log("mcp-drift", "server=%s baseline save failed: %v", m.cfg.Name, err)
		}
		debug.Log("mcp-drift", "server=%s tool definitions changed but allow_tool_drift is set: %+v / -%+v", m.cfg.Name, added, removed)
		return nil, nil, false
	}

	anchor := base.FirstApprovedAt
	if anchor.IsZero() {
		anchor = base.ApprovedAt
	}
	if driftInOnboardingWindow(anchor, time.Now()) {
		if err := store.SaveBaseline(m.cfg.Name, newHash, newNames); err != nil {
			debug.Log("mcp-drift", "server=%s baseline save failed: %v", m.cfg.Name, err)
		}
		debug.Log("mcp-drift", "server=%s tool definitions changed inside 24h onboarding window (baseline updated silently)", m.cfg.Name)
		return nil, nil, false
	}
	if m.OnToolDrift != nil {

		if err := store.SaveBaseline(m.cfg.Name, newHash, newNames); err != nil {
			debug.Log("mcp-drift", "server=%s baseline save failed: %v", m.cfg.Name, err)
		}
	} else {

		debug.Log("mcp-drift", "server=%s TOOL DRIFT with no OnToolDrift sink wired: approved baseline kept (not re-baselined)", m.cfg.Name)
	}
	debug.Log("mcp-drift", "server=%s TOOL DRIFT after approval: added=%v removed=%v", m.cfg.Name, added, removed)
	return added, removed, true
}

func (m *MCPPlugin) notifyToolDrift(added, removed []string) {
	cb := m.OnToolDrift
	if cb == nil {
		return
	}
	server := m.cfg.Name
	safego.Go("plugin.mcp.drift", func() { cb(server, added, removed) })
}
