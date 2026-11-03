package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

type NamedAgentTemplate struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"`
	Tools        []string  `json:"tools,omitempty"`
	BlockedTools []string  `json:"blocked_tools,omitempty"`
	MCPServers   []string  `json:"mcp_servers,omitempty"`
	Model        string    `json:"model,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TemplateStore struct {
	dir string

	touchOnce sync.Once

	mu sync.Mutex
}

func NewTemplateStore(workspace string) *TemplateStore {
	home := config.HomeDir()
	normalized := normalizeWorkspacePath(workspace)
	h := sha256Hash(normalized)
	return &TemplateStore{
		dir: filepath.Join(home, ".ggcode", "subagents", h),
	}
}

func (s *TemplateStore) TemplateDir() string {
	return s.dir
}

func (s *TemplateStore) Save(t NamedAgentTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("create subagent dir: %w", err)
	}
	now := time.Now()

	if existing, err := s.loadByName(t.Name); err == nil && !existing.CreatedAt.IsZero() {
		t.CreatedAt = existing.CreatedAt
	} else if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal template: %w", err)
	}
	path := filepath.Join(s.dir, sanitizeName(t.Name)+".json")

	if raw, rerr := os.ReadFile(path); rerr == nil {
		var onDisk NamedAgentTemplate

		if jerr := json.Unmarshal(raw, &onDisk); jerr == nil {
			if onDisk.Name != "" &&
				strings.TrimSpace(strings.ToLower(onDisk.Name)) != strings.TrimSpace(strings.ToLower(t.Name)) {
				return fmt.Errorf("template name %q collides with existing %q (same sanitized filename); choose a different name", t.Name, onDisk.Name)
			}
		} else {

			debug.Log("subagent", "Save: template file %s is unparseable (%v); overwriting to rebuild it", path, jerr)
		}
	}

	if err := util.AtomicWriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write template: %w", err)
	}
	return nil
}

func (s *TemplateStore) LoadExisting(name string) (NamedAgentTemplate, bool) {
	t, err := s.Load(name)
	if err != nil {
		return NamedAgentTemplate{}, false
	}
	return t, true
}

func (s *TemplateStore) Load(name string) (NamedAgentTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadByName(name)
}

func (s *TemplateStore) loadByName(name string) (NamedAgentTemplate, error) {
	path := filepath.Join(s.dir, sanitizeName(name)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return NamedAgentTemplate{}, fmt.Errorf("load template %q: %w", name, err)
	}
	var t NamedAgentTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return NamedAgentTemplate{}, fmt.Errorf("unmarshal template %q: %w", name, err)
	}

	if t.Name != "" && strings.TrimSpace(strings.ToLower(t.Name)) != strings.TrimSpace(strings.ToLower(name)) {
		return NamedAgentTemplate{}, fmt.Errorf("load template %q: filename collision with %q", name, t.Name)
	}

	s.touchOnce.Do(func() {
		now := time.Now()
		if err := os.Chtimes(s.dir, now, now); err != nil {

			debug.Log("subagent", "template store: Chtimes liveness touch failed for %s: %v", s.dir, err)
		}
	})
	return t, nil
}

func (s *TemplateStore) List() ([]NamedAgentTemplate, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list templates: %w", err)
	}
	var result []NamedAgentTemplate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {

			debug.Log("subagent", "List: skipping unreadable template file %s: %v", entry.Name(), err)
			continue
		}
		var t NamedAgentTemplate
		if jerr := json.Unmarshal(data, &t); jerr != nil {

			debug.Log("subagent", "List: skipping unparseable template file %s: %v", entry.Name(), jerr)
			continue
		}
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *TemplateStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, sanitizeName(name)+".json")
	data, err := os.ReadFile(path)
	if err == nil {
		var stored NamedAgentTemplate

		if jsonErr := json.Unmarshal(data, &stored); jsonErr == nil && stored.Name != "" &&
			strings.TrimSpace(strings.ToLower(stored.Name)) != strings.TrimSpace(strings.ToLower(name)) {
			return fmt.Errorf("template file %q belongs to %q, not %q — refusing to delete the wrong template", sanitizeName(name)+".json", stored.Name, name)
		}
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete template %q: %w", name, err)
	}
	return nil
}

func sanitizeName(name string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return r.Replace(strings.ToLower(strings.TrimSpace(name)))
}

func normalizeWorkspacePath(workspace string) string {
	trimmed := strings.TrimSpace(workspace)
	if trimmed == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(trimmed)
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

func SubagentsRoot() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "subagents")
}

func SweepStaleWorkspaceDirs(currentWorkspace string, olderThan time.Duration) int {
	root := SubagentsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	keep := ""
	if normalized := normalizeWorkspacePath(currentWorkspace); normalized != "" {
		keep = sha256Hash(normalized)
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == keep {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			debug.Log("subagent", "sweep: removing stale workspace dir %s: %v", name, err)
			continue
		}
		removed++
	}
	if removed > 0 {
		debug.Log("subagent", "sweep: removed %d stale subagent workspace dir(s) older than %s", removed, olderThan)
	}
	return removed
}
