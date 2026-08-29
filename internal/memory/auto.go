package memory

import (
	"crypto/sha256"
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
	// #775: sanitizeKey is not injective ("a/b"/"a.b"/"a b" all -> "a-b";
	// pure-CJK keys -> "" -> untitled.md, so ALL Chinese memories shared one
	// file and silently overwrote each other). disambiguateKey appends a short
	// stable hash for keys whose sanitization collides.
	safe := disambiguateKey(key, sanitizeKey(key))
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

// maxInlineBytes is the per-entry size limit for inlining memory content
// directly into the system prompt. Entries larger than this are kept as
// title-only index entries. 1200 bytes ~ 300 tokens - enough for a concise
// build-process note or architecture decision, small enough to stay cheap.
const maxInlineBytes = 1200

// maxTotalInlineBytes is the combined size budget for all inlined memory
// entries. This prevents memory content from consuming too much of the
// context window. 6000 bytes ≈ 1500 tokens.
const maxTotalInlineBytes = 6000

// MemoryEntry pairs a curated memory's metadata with its file content.
type MemoryEntry struct {
	Key     string
	Content string
	Meta    MemoryMeta
}

// LoadForPrompt returns curated memory entries split into two groups:
//
//   - inline: entries whose full content should be injected directly into the
//     system prompt. These are persistent-category entries (architecture
//     decisions, build processes, design docs) that are small enough to fit
//     within the inline budget.
//   - indexOnly: entries whose titles should be listed as a reference index.
//     The LLM can read_file these when needed. This includes transient,
//     evolving, and oversized persistent entries.
//
// This implements relevance-free auto-injection: persistent knowledge from
// previous sessions is immediately available in context without requiring the
// LLM to manually read_file each memory entry.
func (am *AutoMemory) LoadForPrompt() (inline []MemoryEntry, indexOnly []string, err error) {
	metas, err := am.collectMetas()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	active, _, _, _ := curateEntries(metas, now)

	// Sort active entries: persistent first (inline priority), then by key
	// for deterministic output.
	sort.Slice(active, func(i, j int) bool {
		if active[i].Category == CategoryPersistent && active[j].Category != CategoryPersistent {
			return true
		}
		if active[i].Category != CategoryPersistent && active[j].Category == CategoryPersistent {
			return false
		}
		return active[i].Key < active[j].Key
	})

	totalInline := 0
	for _, m := range active {
		path := filepath.Join(am.dir, m.Key+".md")
		data, readErr := os.ReadFile(path)
		content := ""
		if readErr == nil {
			content = strings.TrimSpace(string(data))
		}

		// Inline persistent entries that are small enough and within budget.
		if m.Category == CategoryPersistent && len(content) > 0 && len(content) <= maxInlineBytes && totalInline+len(content) <= maxTotalInlineBytes {
			inline = append(inline, MemoryEntry{
				Key:     m.Key,
				Content: content,
				Meta:    m,
			})
			totalInline += len(content)
		} else {
			indexOnly = append(indexOnly, m.Key)
		}
	}

	return inline, indexOnly, nil
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

// disambiguateKey makes the sanitized filename injective on the original
// key: clean ASCII keys keep their readable form; anything containing other
// characters (spaces, punctuation, CJK) gets a short stable hash suffix so
// distinct keys never map to the same file (#775).
func disambiguateKey(key, safe string) string {
	if safe == "" {
		// Pure non-ASCII key (e.g. Chinese): keep "untitled" readable base,
		// hash still separates different keys.
		safe = "untitled"
	}
	// #1279: injective means sanitize(key) == key VERBATIM. The old charset
	// loop passed keys like "a--b" or "-build-" as injective even though
	// sanitizeKey folds "--"→"-" and trims edges - so "a--b" and "a-b"
	// both landed on a-b.md and the later write silently overwrote the
	// earlier (#775's "distinct keys never map to the same file" promise).
	// Comparing against the folded result catches every mutation: charset
	// replacements, dash collapses, and edge trims all change the string.
	if safe == key {
		return safe
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s-%x", safe, sum[:4])
}
