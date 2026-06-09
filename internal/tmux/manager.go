package tmux

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	sharedManagersMu sync.Mutex
	sharedManagers   = make(map[string]*Manager)
)

// SharedManager returns the process-wide tmux pane manager for a workspace.
// TUI commands and LLM tools use this so they see the same managed panes.
func SharedManager(workspace string) *Manager {
	workspace = normalizeWorkspace(workspace)
	sharedManagersMu.Lock()
	defer sharedManagersMu.Unlock()
	if mgr, ok := sharedManagers[workspace]; ok {
		return mgr
	}
	mgr := NewManager(NewClient(), workspace)
	sharedManagers[workspace] = mgr
	return mgr
}

func resetSharedManagersForTest() {
	sharedManagersMu.Lock()
	defer sharedManagersMu.Unlock()
	sharedManagers = make(map[string]*Manager)
}

// Manager tracks tmux panes created by ggcode for a workspace.
type Manager struct {
	client    *Client
	workspace string
	storePath string

	mu      sync.Mutex
	panes   map[string]Pane
	layouts map[string][]LayoutPane
}

type RestoreResult struct {
	Old Pane
	New Pane
}

func NewManager(client *Client, workspace string) *Manager {
	return NewManagerWithStorePath(client, workspace, defaultPaneStorePath())
}

func NewManagerWithStorePath(client *Client, workspace, storePath string) *Manager {
	if client == nil {
		client = NewClient()
	}
	mgr := &Manager{client: client, workspace: normalizeWorkspace(workspace), storePath: storePath, panes: make(map[string]Pane), layouts: make(map[string][]LayoutPane)}
	_ = mgr.Load()
	return mgr
}

func (m *Manager) Client() *Client {
	if m == nil || m.client == nil {
		return NewClient()
	}
	return m.client
}

func (m *Manager) Workspace() string {
	if m == nil {
		return normalizeWorkspace("")
	}
	return m.workspace
}

func (m *Manager) Detect(ctx context.Context) (*Environment, error) {
	return m.Client().Detect(ctx)
}

func (m *Manager) Split(ctx context.Context, req SplitRequest) (*Pane, error) {
	if strings.TrimSpace(req.Workspace) == "" {
		req.Workspace = m.Workspace()
	}
	pane, err := m.Client().Split(ctx, req)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.panes[pane.ID] = *pane
	m.mu.Unlock()
	_ = m.Save()
	return pane, nil
}

func (m *Manager) Popup(ctx context.Context, req PopupRequest) error {
	if strings.TrimSpace(req.Workspace) == "" {
		req.Workspace = m.Workspace()
	}
	return m.Client().Popup(ctx, req)
}

func (m *Manager) Focus(ctx context.Context, paneID string) error {
	if err := m.Client().Focus(ctx, paneID); err != nil {
		return err
	}
	m.MarkAlive(paneID, true)
	return nil
}

func (m *Manager) Capture(ctx context.Context, paneID string, lines int) (string, error) {
	out, err := m.Client().Capture(ctx, paneID, lines)
	m.MarkAlive(paneID, err == nil)
	return out, err
}

func (m *Manager) Close(ctx context.Context, paneID string) error {
	if err := m.Client().KillPane(ctx, paneID); err != nil {
		m.MarkAlive(paneID, false)
		return err
	}
	m.mu.Lock()
	delete(m.panes, paneID)
	m.mu.Unlock()
	_ = m.Save()
	return nil
}

func (m *Manager) Refresh(ctx context.Context) (int, int, error) {
	aliveIDs, err := m.Client().ListPaneIDs(ctx)
	if err != nil {
		return 0, 0, err
	}
	alive, stale := m.UpdateAliveState(aliveIDs)
	_ = m.Save()
	return alive, stale, nil
}

func (m *Manager) Restore(ctx context.Context, selector string) ([]RestoreResult, error) {
	selector = strings.TrimSpace(selector)
	candidates := m.restoreCandidates(selector)
	results := make([]RestoreResult, 0, len(candidates))
	for _, old := range candidates {
		if strings.TrimSpace(old.Command) == "" {
			continue
		}
		pane, err := m.Client().Split(ctx, SplitRequest{
			Workspace:  m.Workspace(),
			Command:    old.Command,
			Purpose:    old.Purpose,
			Horizontal: old.Horizontal,
			Size:       old.Size,
		})
		if err != nil {
			return results, err
		}
		m.mu.Lock()
		delete(m.panes, old.ID)
		m.panes[pane.ID] = *pane
		m.mu.Unlock()
		results = append(results, RestoreResult{Old: old, New: *pane})
	}
	_ = m.Save()
	return results, nil
}

func (m *Manager) restoreCandidates(selector string) []Pane {
	panes := m.List()
	candidates := make([]Pane, 0, len(panes))
	for _, pane := range panes {
		if pane.Alive {
			continue
		}
		if selector != "" && pane.ID != selector && pane.Purpose != selector {
			continue
		}
		candidates = append(candidates, pane)
	}
	return candidates
}

func (m *Manager) Prune(selector string) int {
	selector = strings.TrimSpace(selector)
	m.mu.Lock()
	removed := 0
	for id, pane := range m.panes {
		if pane.Alive {
			continue
		}
		if selector != "" && pane.ID != selector && pane.Purpose != selector {
			continue
		}
		delete(m.panes, id)
		removed++
	}
	m.mu.Unlock()
	if removed > 0 {
		_ = m.Save()
	}
	return removed
}

func (m *Manager) SaveLayout(name string) error {
	name = normalizeLayoutName(name)
	panes := m.List()
	layout := make([]LayoutPane, 0, len(panes))
	for _, pane := range panes {
		layout = append(layout, LayoutPane{Purpose: pane.Purpose, Command: pane.Command, Horizontal: pane.Horizontal, Size: pane.Size})
	}
	m.mu.Lock()
	if m.layouts == nil {
		m.layouts = make(map[string][]LayoutPane)
	}
	m.layouts[name] = layout
	m.mu.Unlock()
	return m.Save()
}

func (m *Manager) ListLayoutNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.layouts))
	for name := range m.layouts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) ListLayouts() map[string][]LayoutPane {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneLayouts(m.layouts)
}

func (m *Manager) Layout(name string) []LayoutPane {
	name = normalizeLayoutName(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]LayoutPane(nil), m.layouts[name]...)
}

func (m *Manager) SetupLayout(ctx context.Context, name string) ([]Pane, error) {
	layout := m.Layout(name)
	created := make([]Pane, 0, len(layout))
	for _, spec := range layout {
		if m.hasAlivePaneForSpec(spec) {
			continue
		}
		pane, err := m.Split(ctx, SplitRequest{Workspace: m.Workspace(), Command: spec.Command, Purpose: spec.Purpose, Horizontal: spec.Horizontal, Size: spec.Size})
		if err != nil {
			return created, err
		}
		created = append(created, *pane)
	}
	return created, nil
}

func (m *Manager) hasAlivePaneForSpec(spec LayoutPane) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, pane := range m.panes {
		if pane.Alive && pane.Purpose == spec.Purpose && pane.Command == spec.Command {
			return true
		}
	}
	return false
}

func normalizeLayoutName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	return name
}

func (m *Manager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.panes)
}

func (m *Manager) List() []Pane {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	panes := make([]Pane, 0, len(m.panes))
	for _, pane := range m.panes {
		panes = append(panes, pane)
	}
	sort.Slice(panes, func(i, j int) bool { return panes[i].ID < panes[j].ID })
	return panes
}

func (m *Manager) HasPanes() bool {
	return m.Count() > 0
}

func (m *Manager) MarkAlive(paneID string, alive bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if pane, ok := m.panes[paneID]; ok {
		pane.Alive = alive
		m.panes[paneID] = pane
	}
	m.mu.Unlock()
	_ = m.Save()
}

func (m *Manager) UpdateAliveState(aliveIDs map[string]struct{}) (int, int) {
	if m == nil {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	alive := 0
	stale := 0
	for id, pane := range m.panes {
		_, ok := aliveIDs[id]
		pane.Alive = ok
		m.panes[id] = pane
		if ok {
			alive++
		} else {
			stale++
		}
	}
	return alive, stale
}

func (m *Manager) ManagedPaneText() string {
	panes := m.List()
	if len(panes) == 0 {
		return "tmux managed panes: none"
	}
	var b strings.Builder
	b.WriteString("tmux managed panes:\n")
	for _, pane := range panes {
		state := "stale"
		if pane.Alive {
			state = "alive"
		}
		b.WriteString(fmt.Sprintf("- %s [%s/%s] %s\n", pane.ID, pane.Purpose, state, pane.Command))
	}
	return strings.TrimSpace(b.String())
}

func (m *Manager) Load() error {
	if m == nil || strings.TrimSpace(m.storePath) == "" {
		return nil
	}
	panes, layouts, err := loadWorkspaceState(m.storePath, m.workspace)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.panes = make(map[string]Pane, len(panes))
	for _, pane := range panes {
		m.panes[pane.ID] = pane
	}
	m.layouts = cloneLayouts(layouts)
	if m.layouts == nil {
		m.layouts = make(map[string][]LayoutPane)
	}
	return nil
}

func (m *Manager) Save() error {
	if m == nil || strings.TrimSpace(m.storePath) == "" {
		return nil
	}
	return saveWorkspaceState(m.storePath, m.workspace, m.List(), m.ListLayouts())
}

func normalizeWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		return workspace
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
