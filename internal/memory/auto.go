package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// AutoMemory manages automatic memory persistence in ~/.ggcode/memory/.
type AutoMemory struct {
	dir string
}

// NewAutoMemory creates an AutoMemory instance for global memory (~/.ggcode/memory/).
func NewAutoMemory() *AutoMemory {
	home := config.HomeDir()
	dir := filepath.Join(home, ".ggcode", "memory")
	_ = os.MkdirAll(dir, 0755)
	return &AutoMemory{dir: dir}
}

// NewProjectAutoMemory creates an AutoMemory instance for project-scoped memory.
// Uses <workingDir>/.ggcode/memory/ directly — no parent directory traversal.
// Returns nil only if workingDir is the user's HOME directory.
func NewProjectAutoMemory(workingDir string) *AutoMemory {
	home := config.HomeDir()
	// Never treat HOME as a project root to avoid polluting ~/.ggcode/
	if strings.EqualFold(workingDir, home) {
		return nil
	}
	dir := filepath.Join(workingDir, ".ggcode", "memory")
	_ = os.MkdirAll(dir, 0755)
	return &AutoMemory{dir: dir}
}

// SaveMemory saves a memory entry to ~/.ggcode/memory/{key}.md.
func (am *AutoMemory) SaveMemory(key, content string) error {
	// Sanitize key for use as filename
	safe := sanitizeKey(key)
	if safe == "" {
		safe = "untitled"
	}
	path := filepath.Join(am.dir, safe+".md")
	return os.WriteFile(path, []byte(content), 0644)
}

// LoadIndex loads all memory file keys and returns a formatted index (titles
// only) plus the list of file paths. Applies curation filtering (expiry +
// dedup) so the system prompt only shows active memories.
func (am *AutoMemory) LoadIndex() (string, []string, error) {
	metas, err := am.collectMetas()
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	active, expired, deduped, capped := curateEntries(metas, now)
	debug.Log("memory", "%s", formatMemorySummary(len(metas), len(active), expired, deduped, capped))

	var keys, files []string
	for _, m := range active {
		keys = append(keys, m.Key)
		files = append(files, filepath.Join(am.dir, m.Key+".md"))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(fmt.Sprintf("- %s\n", key))
	}
	return strings.TrimSpace(builder.String()), files, nil
}

// LoadAll loads all memory files and returns their combined content.
// Applies curation filtering (expiry + dedup) so only active memories
// are injected into the LLM context.
func (am *AutoMemory) LoadAll() (string, []string, error) {
	metas, err := am.collectMetas()
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	active, expired, deduped, capped := curateEntries(metas, now)
	debug.Log("memory", "%s", formatMemorySummary(len(metas), len(active), expired, deduped, capped))

	var files []string
	var builder strings.Builder
	for _, m := range active {
		path := filepath.Join(am.dir, m.Key+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		builder.WriteString(fmt.Sprintf("### %s\n%s\n\n", m.Key, string(data)))
		files = append(files, path)
	}

	return strings.TrimSpace(builder.String()), files, nil
}

// List returns all memory keys (unfiltered — includes expired/deduped).
func (am *AutoMemory) List() ([]string, error) {
	entries, err := os.ReadDir(am.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		keys = append(keys, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(keys)
	return keys, nil
}

// collectMetas reads the memory directory and returns MemoryMeta for each .md file.
func (am *AutoMemory) collectMetas() ([]MemoryMeta, error) {
	entries, err := os.ReadDir(am.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var metas []MemoryMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		metas = append(metas, buildMemoryMeta(key, info.ModTime()))
	}
	return metas, nil
}

// Clear removes all memory files.
func (am *AutoMemory) Clear() error {
	entries, err := os.ReadDir(am.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(am.dir, e.Name()))
	}
	return nil
}

// Dir returns the memory directory path.
func (am *AutoMemory) Dir() string {
	return am.dir
}

func sanitizeKey(key string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, key)
	// Collapse consecutive dashes and trim
	for strings.Contains(safe, "--") {
		safe = strings.ReplaceAll(safe, "--", "-")
	}
	return strings.Trim(safe, "-")
}
