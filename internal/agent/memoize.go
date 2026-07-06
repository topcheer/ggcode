package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// memoizeCache stores results of read-only tool calls to avoid redundant
// re-execution when the agent re-calls the same tool after context clearing.
//
// Inspired by ToolCaching (arXiv:2601.15335) which found 40%+ of LLM tool
// calls are redundant. In ggcode, this happens when tool-result clearing
// replaces old results with placeholders, and the agent re-calls the tool.
//
// Invalidation strategy:
//   - File-based tools (read_file): check file mtime — if unchanged, result is fresh
//   - Directory tools (list_directory, glob): check directory mtime
//   - Search tools (grep, search_files): 30s TTL (results may change as files are edited)
//   - LSP tools: 15s TTL (LSP server state changes as code is edited)
//   - Git read tools: 10s TTL (working tree changes)
//
// This is complementary to speculative execution (predicts next call) and
// parallel pre-execution (pre-runs all read-only tools in a batch).

const (
	memoizeMaxEntries = 50 // bounded LRU
	memoizeSearchTTL  = 30 * time.Second
	memoizeLSPTTL     = 15 * time.Second
	memoizeGitTTL     = 10 * time.Second
)

type memoEntry struct {
	result    tool.Result
	createdAt time.Time
	mtime     time.Time // file modification time at time of execution (for file-based tools)
	path      string    // file/dir path for mtime invalidation (empty = TTL-only)
}

type toolMemo struct {
	mu      sync.Mutex
	entries map[string]*memoEntry // key: SHA256(toolName + args)
	order   []string              // LRU ordering (oldest first)
	hits    int
	misses  int
}

func newToolMemo() *toolMemo {
	return &toolMemo{
		entries: make(map[string]*memoEntry),
	}
}

func (m *toolMemo) key(toolName string, args []byte) string {
	h := sha256.Sum256(append([]byte(toolName+":"), args...))
	return hex.EncodeToString(h[:])
}

// extractPathForInvalidation extracts the file/dir path from tool arguments
// for mtime-based invalidation. Returns empty string for tools that use TTL-only.
func extractPathForInvalidation(toolName string, args []byte) string {
	switch toolName {
	case "read_file":
		return extractJSONStringField(args, "path")
	case "list_directory":
		return extractJSONStringField(args, "path")
	case "glob":
		// Glob supports recursive patterns (**), so the top-level directory's
		// mtime does not reflect changes in subdirectories. Use TTL instead.
		return ""
	case "multi_file_read":
		return "" // multi-file: skip per-file mtime, use TTL
	default:
		return ""
	}
}

// getTTL returns the TTL for a tool. Returns 0 for file-based tools that
// use mtime invalidation instead.
func (m *toolMemo) getTTL(toolName string) time.Duration {
	switch toolName {
	case "read_file", "list_directory":
		return 0 // use mtime invalidation, no TTL
	case "grep", "search_files", "glob":
		return memoizeSearchTTL
	case "lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_workspace_symbols", "lsp_diagnostics", "lsp_implementation",
		"lsp_incoming_calls", "lsp_outgoing_calls":
		return memoizeLSPTTL
	case "git_status", "git_diff", "git_log", "git_branch_list",
		"git_show", "git_blame":
		return memoizeGitTTL
	default:
		return memoizeSearchTTL // conservative default
	}
}

// get returns a cached result if valid, or (zero, false).
func (m *toolMemo) get(toolName string, args []byte) (tool.Result, bool) {
	k := m.key(toolName, args)
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[k]
	if !ok {
		m.misses++
		return tool.Result{}, false
	}

	// Check validity.
	if entry.path != "" {
		// File-based invalidation: check mtime.
		info, err := os.Stat(entry.path)
		if err != nil {
			// File doesn't exist anymore — cache miss.
			m.removeLocked(k)
			m.misses++
			return tool.Result{}, false
		}
		if !info.ModTime().Equal(entry.mtime) {
			// File changed — cache miss.
			m.removeLocked(k)
			m.misses++
			return tool.Result{}, false
		}
	} else {
		// TTL-based invalidation.
		ttl := m.getTTL(toolName)
		if time.Since(entry.createdAt) > ttl {
			m.removeLocked(k)
			m.misses++
			return tool.Result{}, false
		}
	}

	// Move to end of LRU order.
	m.touchLocked(k)
	m.hits++
	return entry.result, true
}

// put stores a tool result in the memo.
func (m *toolMemo) put(toolName string, args []byte, result tool.Result) {
	if result.IsError {
		return // don't cache errors
	}
	k := m.key(toolName, args)
	path := extractPathForInvalidation(toolName, args)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Evict if at capacity.
	for len(m.entries) >= memoizeMaxEntries {
		if len(m.order) == 0 {
			break
		}
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.entries, oldest)
	}

	entry := &memoEntry{
		result:    result,
		createdAt: time.Now(),
		path:      path,
	}
	if path != "" {
		if info, err := os.Stat(path); err == nil {
			entry.mtime = info.ModTime()
		}
	}

	m.entries[k] = entry
	m.order = append(m.order, k)
}

func (m *toolMemo) removeLocked(k string) {
	delete(m.entries, k)
	for i, key := range m.order {
		if key == k {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}

func (m *toolMemo) touchLocked(k string) {
	for i, key := range m.order {
		if key == k {
			m.order = append(m.order[:i], m.order[i+1:]...)
			m.order = append(m.order, k)
			break
		}
	}
}

// reset clears all entries (called at start of each new run).
func (m *toolMemo) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]*memoEntry)
	m.order = nil
}

// extractJSONStringField extracts a string field from JSON args.
// Returns empty string if the field is not found or on error.
func extractJSONStringField(data []byte, field string) string {
	if len(data) == 0 {
		return ""
	}
	// Simple JSON extraction without full parsing to avoid import cycles.
	// Look for "field" : "value" pattern (with optional whitespace).
	prefix := `"` + field + `"`
	idx := indexOfBytes(data, []byte(prefix))
	if idx < 0 {
		return ""
	}
	// Skip past the field name to find the colon and opening quote.
	pos := idx + len(prefix)
	// Skip whitespace
	for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n') {
		pos++
	}
	if pos >= len(data) || data[pos] != ':' {
		return ""
	}
	pos++ // skip colon
	// Skip whitespace
	for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n') {
		pos++
	}
	if pos >= len(data) || data[pos] != '"' {
		return ""
	}
	start := pos + 1 // skip opening quote
	end := start
	for end < len(data) {
		if data[end] == '"' {
			break
		}
		if data[end] == '\\' && end+1 < len(data) {
			end += 2
			continue
		}
		end++
	}
	if end > len(data) {
		return ""
	}
	path := string(data[start:end])
	// Clean the path for filesystem use.
	return filepath.Clean(path)
}

func indexOfBytes(haystack, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	if len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
