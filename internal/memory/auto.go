package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AutoMemory manages automatic memory persistence in ~/.ggcode/memory/.
type AutoMemory struct {
	dir string
}

// NewAutoMemory creates an AutoMemory instance.
func NewAutoMemory() *AutoMemory {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".ggcode", "memory")
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

// LoadAll loads all memory files and returns their combined content.
func (am *AutoMemory) LoadAll() (string, []string, error) {
	entries, err := os.ReadDir(am.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, err
	}

	var files []string
	var builder strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(am.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		builder.WriteString(fmt.Sprintf("### %s\n%s\n\n", key, string(data)))
		files = append(files, path)
	}

	return strings.TrimSpace(builder.String()), files, nil
}

// List returns all memory keys.
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
