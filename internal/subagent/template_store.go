package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	// projectDir holds repo-committable, team-shared templates from
	// <workspace>/.ggcode/agents (r555: agents-as-code, mirrors the project-
	// level .claude/agents convention and ggcode's own .ggcode/commands /
	// .agents/skills precedents). Empty when the workspace has no such
	// directory — behavior is then byte-identical to the home-only store.
	// Project templates shadow same-named user templates; Save/Delete refuse
	// project-scoped names so a user-side write can never be silently
	// masked by (or destroy) the committed definition.
	projectDir string
	// mu serializes Save/Delete against each other (single-process domain).
	// #3100 V2: Save's Load→collision-check→write and Delete's
	// check-then-remove were TOCTOU-unsynchronized — two in-process callers
	// could both pass the collision check and interleave writes, and Delete
	// could remove a template a concurrent Save had just replaced. Cross-
	// PROCESS sharing (~/.ggcode on a multi-instance LAN host) is out of
	// scope: same-instance locking plus atomic rename keeps any single
	// writer's file internally consistent; last-writer-wins across processes
	// is the documented boundary.
	mu sync.Mutex
}

// NewTemplateStore creates a store for the given workspace.
// Templates are stored under ~/.ggcode/subagents/<workspace-hash>/.
// If the workspace contains a .ggcode/agents directory, those templates are
// also visible (project-scoped, shadowing same-named user templates).
func NewTemplateStore(workspace string) *TemplateStore {
	home := config.HomeDir()
	normalized := normalizeWorkspacePath(workspace)
	h := sha256Hash(normalized)
	s := &TemplateStore{
		dir: filepath.Join(home, ".ggcode", "subagents", h),
	}
	if normalized != "" {
		projectDir := filepath.Join(normalized, ".ggcode", "agents")
		if info, err := os.Stat(projectDir); err == nil && info.IsDir() {
			s.projectDir = projectDir
		}
	}
	return s
}

// TemplateDir returns the on-disk directory where templates are stored.
// Exposed for testing and diagnostics.
func (s *TemplateStore) TemplateDir() string {
	return s.dir
}

// Save creates or updates a template by name.
func (s *TemplateStore) Save(t NamedAgentTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// r555: a project-scoped file at this name means the effective template
	// comes from the repo, not the user library — writing here would create a
	// permanently shadowed duplicate. Point the caller at the committed file.
	if s.projectDir != "" {
		if projectPath := filepath.Join(s.projectDir, sanitizeName(t.Name)+".json"); fileExists(projectPath) {
			return fmt.Errorf("template %q is project-scoped (%s); edit that file directly or choose a different name", t.Name, projectPath)
		}
	}
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("create subagent dir: %w", err)
	}
	now := time.Now()
	// Preserve CreatedAt from existing template on updates — but only when
	// the existing file really is this template. sanitizeName collides across
	// names like "Code Reviewer" and "code_reviewer"; Load now enforces the
	// name match, so a colliding different-name file returns ErrNotFound and
	// we refuse to overwrite it below (#230).
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
	// Refuse to silently overwrite a different template whose name sanitizes
	// to the same filename — that destroyed the other template's prompt and
	// inherited its CreatedAt (#230).
	if raw, rerr := os.ReadFile(path); rerr == nil {
		var onDisk NamedAgentTemplate
		// #280: use the same normalized comparison as Load (below) —
		// case/whitespace-only renames of the same template must be
		// allowed as updates, not rejected as collisions.
		if jerr := json.Unmarshal(raw, &onDisk); jerr == nil {
			if onDisk.Name != "" &&
				strings.TrimSpace(strings.ToLower(onDisk.Name)) != strings.TrimSpace(strings.ToLower(t.Name)) {
				return fmt.Errorf("template name %q collides with existing %q (same sanitized filename); choose a different name", t.Name, onDisk.Name)
			}
		} else {
			// #3100: an unparseable file used to fall through to a SILENT
			// overwrite that skipped the collision check entirely — the
			// on-disk template was lost with no warning. Overwriting is the
			// right recovery (the file is corrupt), but never silently.
			debug.Log("subagent", "Save: template file %s is unparseable (%v); overwriting to rebuild it", path, jerr)
		}
	}
	// #3100 V1: write via the shared atomic-write contract — os.WriteFile
	// truncates first, so a crash or full disk mid-write left a half-written
	// JSON that then silently vanished from List and bypassed the collision
	// check above on the next Save. AtomicWriteFile (tmp file in the same
	// dir + fsync + rename, #1359 symlink semantics) matches the 25+ other
	// state-store writers; the same fix was independently prepared on the
	// r132-frontier audit branch.
	if err := util.AtomicWriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write template: %w", err)
	}
	return nil
}

// LoadExisting checks if a template exists and returns it with a boolean
// indicating existence. Unlike Load, it does not return an error for missing templates.
func (s *TemplateStore) LoadExisting(name string) (NamedAgentTemplate, bool) {
	t, err := s.Load(name)
	if err != nil {
		return NamedAgentTemplate{}, false
	}
	return t, true
}

// Load reads a template by name. Returns error if not found.
func (s *TemplateStore) Load(name string) (NamedAgentTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadByName(name)
}

// loadByName is the lock-free core of Load — callers hold s.mu. Project
// templates take priority; a miss (file absent) falls through to the user
// library, while a present-but-broken project file surfaces its error.
func (s *TemplateStore) loadByName(name string) (NamedAgentTemplate, error) {
	if s.projectDir != "" {
		t, err := loadTemplateFromDir(s.projectDir, name)
		if err == nil {
			return t, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return NamedAgentTemplate{}, err
		}
	}
	return loadTemplateFromDir(s.dir, name)
}

// loadTemplateFromDir reads one template by name from a single directory,
// applying the #230 collision guard. Read errors are wrapped with %w so
// callers can distinguish a missing file via errors.Is(err, fs.ErrNotExist).
func loadTemplateFromDir(dir, name string) (NamedAgentTemplate, error) {
	path := filepath.Join(dir, sanitizeName(name)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return NamedAgentTemplate{}, fmt.Errorf("load template %q: %w", name, err)
	}
	var t NamedAgentTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return NamedAgentTemplate{}, fmt.Errorf("unmarshal template %q: %w", name, err)
	}
	// sanitizeName collides across names ("Code Reviewer" vs "code_reviewer").
	// If the file on disk holds a different template's name, treat it as not
	// found rather than returning the wrong template (#230).
	if t.Name != "" && strings.TrimSpace(strings.ToLower(t.Name)) != strings.TrimSpace(strings.ToLower(name)) {
		return NamedAgentTemplate{}, fmt.Errorf("load template %q: filename collision with %q", name, t.Name)
	}
	return t, nil
}

// fileExists is a small helper for the project-scope guards in Save/Delete.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// List returns all templates, sorted by name. Project-scoped templates
// shadow same-named user templates (normalized-name key).
func (s *TemplateStore) List() ([]NamedAgentTemplate, error) {
	userTemplates, err := listTemplatesInDir(s.dir)
	if err != nil {
		return nil, err
	}
	if s.projectDir == "" {
		sortTemplates(userTemplates)
		return userTemplates, nil
	}
	projectTemplates, err := listTemplatesInDir(s.projectDir)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]NamedAgentTemplate, len(userTemplates)+len(projectTemplates))
	for _, t := range userTemplates {
		merged[templateKey(t.Name)] = t
	}
	for _, t := range projectTemplates {
		merged[templateKey(t.Name)] = t // project wins on collision
	}
	result := make([]NamedAgentTemplate, 0, len(merged))
	for _, t := range merged {
		result = append(result, t)
	}
	sortTemplates(result)
	return result, nil
}

// listTemplatesInDir reads every parseable *.json template in dir; a missing
// directory is an empty list. Read/unmarshal failures are logged and skipped
// (#3100: never let one bad file hide the rest).
func listTemplatesInDir(dir string) ([]NamedAgentTemplate, error) {
	entries, err := os.ReadDir(dir)
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
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
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
	return result, nil
}

func templateKey(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func sortTemplates(ts []NamedAgentTemplate) {
	sort.Slice(ts, func(i, j int) bool {
		return ts[i].Name < ts[j].Name
	})
}

// Delete removes a template by name.
func (s *TemplateStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// r555: project-scoped templates live in the repo — deleting them from
	// here would either fail on the user-library path or, worse, succeed at
	// removing a shadowed user duplicate while leaving the project file in
	// place. Point the caller at the committed file instead.
	if s.projectDir != "" {
		if projectPath := filepath.Join(s.projectDir, sanitizeName(name)+".json"); fileExists(projectPath) {
			return fmt.Errorf("template %q is project-scoped (%s); remove that file from the repository instead", name, projectPath)
		}
	}
	// #812: mirror Load's collision guard — 'Code Reviewer' and
	// 'code_reviewer' sanitize to the same file; deleting the wrong casing
	// permanently destroyed the other template and reported success.
	path := filepath.Join(s.dir, sanitizeName(name)+".json")
	data, err := os.ReadFile(path)
	if err == nil {
		var stored NamedAgentTemplate
		// #1633 case 4: normalized comparison like Save/Load — the bare
		// compare made Delete("code reviewer") refuse what Load accepts
		// (Save("Code Reviewer") loads case/whitespace-insensitively but
		// would not delete).
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

// SubagentsRoot returns ~/.ggcode/subagents - the directory holding one
// workspace-hash subdirectory per workspace that ever used named agents.
func SubagentsRoot() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "subagents")
}

// SweepStaleWorkspaceDirs removes workspace-hash directories under
// ~/.ggcode/subagents that have not been touched for olderThan, keeping the
// current workspace's own directory (and anything newer than the cutoff).
//
// #3341 (sa-245 audit): every distinct workspace path creates a sha256-named
// directory and NOTHING ever removed them - a real profile held 3445 of them,
// most containing a single test artifact. The hash is not reversible, so
// users cannot even tell which directory belongs to which workspace.
// Directory mtime is the liveness signal: Save/Delete/Clean rewrite files
// inside, which updates the parent dir mtime on file create/remove. A
// conservative 90d cutoff matches the session retention default (#3337).
func SweepStaleWorkspaceDirs(currentWorkspace string, olderThan time.Duration) int {
	root := SubagentsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0 // no subagents dir yet - nothing to sweep
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
			continue // never age out the workspace we are running in
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue // unreadable or recently active - keep
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
