package tmux

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type LayoutPane struct {
	Purpose    string `json:"purpose"`
	Command    string `json:"command"`
	Horizontal bool   `json:"horizontal"`
	Size       string `json:"size"`
}

type paneStoreFile struct {
	Workspaces map[string]workspacePaneStore `json:"workspaces"`
}

type workspacePaneStore struct {
	Workspace string                  `json:"workspace"`
	Panes     []Pane                  `json:"panes"`
	Layouts   map[string][]LayoutPane `json:"layouts,omitempty"`
}

func defaultPaneStorePath() string {
	home := os.Getenv("HOME")
	if strings.TrimSpace(home) == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	if strings.TrimSpace(home) == "" {
		return filepath.Join(".ggcode", "tmux-panes.json")
	}
	return filepath.Join(home, ".ggcode", "tmux-panes.json")
}

func workspaceKey(workspace string) string {
	sum := sha256.Sum256([]byte(normalizeWorkspace(workspace)))
	return hex.EncodeToString(sum[:])
}

func loadWorkspaceState(path, workspace string) ([]Pane, map[string][]LayoutPane, error) {
	store, err := readPaneStore(path)
	if err != nil {
		return nil, nil, err
	}
	entry, ok := store.Workspaces[workspaceKey(workspace)]
	if !ok {
		return nil, nil, nil
	}
	panes := make([]Pane, 0, len(entry.Panes))
	for _, pane := range entry.Panes {
		if strings.TrimSpace(pane.ID) == "" {
			continue
		}
		if strings.TrimSpace(pane.Workspace) == "" {
			pane.Workspace = entry.Workspace
		}
		panes = append(panes, pane)
	}
	layouts := cloneLayouts(entry.Layouts)
	return panes, layouts, nil
}

func saveWorkspaceState(path, workspace string, panes []Pane, layouts map[string][]LayoutPane) error {
	// Acquire cross-process lock — multiple workspaces share the same store file.
	unlock, err := lockTmuxStore(path)
	if err != nil {
		return fmt.Errorf("lock tmux pane store: %w", err)
	}
	defer unlock()

	store, err := readPaneStore(path)
	if err != nil {
		return err
	}
	if store.Workspaces == nil {
		store.Workspaces = make(map[string]workspacePaneStore)
	}
	workspace = normalizeWorkspace(workspace)
	key := workspaceKey(workspace)
	filtered := make([]Pane, 0, len(panes))
	for _, pane := range panes {
		if strings.TrimSpace(pane.ID) == "" {
			continue
		}
		if strings.TrimSpace(pane.Workspace) == "" {
			pane.Workspace = workspace
		}
		filtered = append(filtered, pane)
	}
	store.Workspaces[key] = workspacePaneStore{Workspace: workspace, Panes: filtered, Layouts: cloneLayouts(layouts)}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tmux pane store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create tmux pane store directory: %w", err)
	}
	// Use temp+rename for atomic write — os.WriteFile truncates first,
	// concurrent readers could see empty/partial JSON.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write tmux pane store: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmux pane store: %w", err)
	}
	return nil
}

func cloneLayouts(layouts map[string][]LayoutPane) map[string][]LayoutPane {
	if len(layouts) == 0 {
		return nil
	}
	clone := make(map[string][]LayoutPane, len(layouts))
	for name, panes := range layouts {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		clone[name] = append([]LayoutPane(nil), panes...)
	}
	return clone
}

func readPaneStore(path string) (paneStoreFile, error) {
	var store paneStoreFile
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		store.Workspaces = make(map[string]workspacePaneStore)
		return store, nil
	}
	if err != nil {
		return store, fmt.Errorf("read tmux pane store: %w", err)
	}
	if len(data) == 0 {
		store.Workspaces = make(map[string]workspacePaneStore)
		return store, nil
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return store, fmt.Errorf("parse tmux pane store: %w", err)
	}
	if store.Workspaces == nil {
		store.Workspaces = make(map[string]workspacePaneStore)
	}
	return store, nil
}

// storeLocks serializes read-modify-write on the shared pane store file.
// Multiple workspace Managers share the same file path but have
// independent mutexes — this package-level lock ensures atomicity.
var storeLocks sync.Mutex

func lockTmuxStore(_ string) (func(), error) {
	storeLocks.Lock()
	return func() { storeLocks.Unlock() }, nil
}
