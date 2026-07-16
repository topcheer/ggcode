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
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// NamedAgentTemplate is a persisted subagent configuration that defines
// a reusable agent profile with its own system prompt, tools, and model.
type NamedAgentTemplate struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"`
	Tools        []string  `json:"tools,omitempty"`         // allowlist; empty = all (minus blocked)
	BlockedTools []string  `json:"blocked_tools,omitempty"` // denylist, applied after allowlist
	MCPServers   []string  `json:"mcp_servers,omitempty"`   // MCP server names to include; empty = none
	Model        string    `json:"model,omitempty"`         // model name; empty = inherit parent
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TemplateStore manages named agent templates on disk, scoped per workspace.
type TemplateStore struct {
	dir string
}

// NewTemplateStore creates a store for the given workspace.
// Templates are stored under ~/.ggcode/subagents/<workspace-hash>/.
func NewTemplateStore(workspace string) *TemplateStore {
	home := config.HomeDir()
	normalized := normalizeWorkspacePath(workspace)
	h := sha256Hash(normalized)
	return &TemplateStore{
		dir: filepath.Join(home, ".ggcode", "subagents", h),
	}
}

// Save creates or updates a template by name.
func (s *TemplateStore) Save(t NamedAgentTemplate) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("create subagent dir: %w", err)
	}
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal template: %w", err)
	}
	path := filepath.Join(s.dir, sanitizeName(t.Name)+".json")
	return os.WriteFile(path, data, 0644)
}

// Load reads a template by name. Returns error if not found.
func (s *TemplateStore) Load(name string) (NamedAgentTemplate, error) {
	path := filepath.Join(s.dir, sanitizeName(name)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return NamedAgentTemplate{}, fmt.Errorf("load template %q: %w", name, err)
	}
	var t NamedAgentTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return NamedAgentTemplate{}, fmt.Errorf("unmarshal template %q: %w", name, err)
	}
	return t, nil
}

// List returns all templates, sorted by name.
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
			continue
		}
		var t NamedAgentTemplate
		if json.Unmarshal(data, &t) == nil {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// Delete removes a template by name.
func (s *TemplateStore) Delete(name string) error {
	path := filepath.Join(s.dir, sanitizeName(name)+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete template %q: %w", name, err)
	}
	return nil
}

// sanitizeName converts a template name to a safe filename.
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
