package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	memoizeMaxEntries = 50
	memoizeSearchTTL  = 30 * time.Second
	memoizeLSPTTL     = 15 * time.Second
	memoizeGitTTL     = 10 * time.Second
	memoizeDirListTTL = 5 * time.Second
)

type memoEntry struct {
	result    tool.Result
	createdAt time.Time
	mtime     time.Time
	path      string
	size      int64
	maxAge    time.Duration
}

type toolMemo struct {
	mu      sync.Mutex
	entries map[string]*memoEntry
	order   []string
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

func extractPathForInvalidation(toolName string, args []byte) string {
	switch toolName {
	case "read_file":
		return extractJSONStringField(args, "path")
	case "list_directory":
		return extractJSONStringField(args, "path")
	case "glob":

		return ""
	case "multi_file_read":
		return ""
	default:
		return ""
	}
}

func (m *toolMemo) getTTL(toolName string) time.Duration {
	switch toolName {
	case "read_file", "list_directory":
		return 0
	case "grep", "search_files", "glob", "code_search":
		return memoizeSearchTTL
	case "lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_workspace_symbols", "lsp_diagnostics", "lsp_implementation",
		"lsp_incoming_calls", "lsp_outgoing_calls", "lsp_document_highlights":
		return memoizeLSPTTL
	case "git_status", "git_diff", "git_log", "git_branch_list",
		"git_show", "git_blame":
		return memoizeGitTTL
	default:
		return memoizeSearchTTL
	}
}

func (m *toolMemo) get(toolName string, args []byte) (tool.Result, bool) {
	k := m.key(toolName, args)
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[k]
	if !ok {
		m.misses++
		return tool.Result{}, false
	}

	if entry.path != "" {

		info, err := os.Stat(entry.path)
		if err != nil {

			m.removeLocked(k)
			m.misses++
			return tool.Result{}, false
		}
		if !info.ModTime().Equal(entry.mtime) {

			m.removeLocked(k)
			m.misses++
			return tool.Result{}, false
		}

		if info.Size() != entry.size {
			m.removeLocked(k)
			m.misses++
			return tool.Result{}, false
		}
	} else {

		ttl := m.getTTL(toolName)
		if time.Since(entry.createdAt) > ttl {
			m.removeLocked(k)
			m.misses++
			return tool.Result{}, false
		}
	}

	if entry.maxAge > 0 && time.Since(entry.createdAt) > entry.maxAge {
		m.removeLocked(k)
		m.misses++
		return tool.Result{}, false
	}

	m.touchLocked(k)
	m.hits++
	return entry.result, true
}

func (m *toolMemo) put(toolName string, args []byte, result tool.Result) {
	if result.IsError {
		return
	}
	k := m.key(toolName, args)
	path := extractPathForInvalidation(toolName, args)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[k]; exists {
		m.removeLocked(k)
	}

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
			entry.size = info.Size()
		}
	}

	if toolName == "list_directory" {
		entry.maxAge = memoizeDirListTTL
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

func (m *toolMemo) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]*memoEntry)
	m.order = nil
}

func (m *toolMemo) invalidateAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := len(m.entries)
	m.entries = make(map[string]*memoEntry)
	m.order = nil
	if count > 0 {
		debug.Log("memoize", "invalidateAll: cleared %d entries (whole-tree git operation)", count)
	}
}

func (m *toolMemo) invalidateTTLBased() {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleared := 0
	kept := 0
	newOrder := make([]string, 0, len(m.order))
	for _, k := range m.order {
		entry, ok := m.entries[k]
		if !ok {
			continue
		}
		if entry.path != "" {

			newOrder = append(newOrder, k)
			kept++
		} else {

			delete(m.entries, k)
			cleared++
		}
	}
	m.order = newOrder
	if cleared > 0 {
		debug.Log("memoize", "invalidateTTLBased: cleared %d TTL entries, kept %d mtime entries", cleared, kept)
	}
}

func extractJSONStringField(data []byte, field string) string {
	if len(data) == 0 {
		return ""
	}

	prefix := `"` + field + `"`
	idx := indexOfBytes(data, []byte(prefix))
	if idx < 0 {
		return ""
	}

	pos := idx + len(prefix)

	for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n') {
		pos++
	}
	if pos >= len(data) || data[pos] != ':' {
		return ""
	}
	pos++

	for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n') {
		pos++
	}
	if pos >= len(data) || data[pos] != '"' {
		return ""
	}
	start := pos + 1
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
