package permission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	autoApproveThreshold = 3

	maxTrackedKeys = 200
)

type approvalEntry struct {
	consecutive  int
	autoApproved bool
}

type ApprovalMemory struct {
	mu    sync.RWMutex
	store map[string]*approvalEntry

	lastMode PermissionMode
}

func NewApprovalMemory() *ApprovalMemory {
	return &ApprovalMemory{
		store: make(map[string]*approvalEntry),
	}
}

func pathSignature(path string) string {
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	if ext == "" {

		return dir
	}

	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test"+ext) {
		return filepath.Join(dir, "*_test"+ext)
	}
	return filepath.Join(dir, "*"+ext)
}

func commandSignature(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}

	if strings.ContainsAny(cmd, ";|&\n$`") || strings.Contains(cmd, "$(") {
		return cmd + ":no-auto-approve:chained"
	}

	if strings.Contains(cmd, ">") || strings.Contains(cmd, "<") {
		return cmd + ":redirect"
	}
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return ""
	}

	if commandHasDestructiveFlag(tokens) {
		return strings.Join(tokens, " ")
	}
	if len(tokens) >= 2 {
		return tokens[0] + " " + tokens[1]
	}
	return tokens[0]
}

func commandHasDestructiveFlag(tokens []string) bool {
	for _, tok := range tokens[1:] {
		t := strings.ToLower(strings.TrimLeft(tok, "-"))
		switch t {
		case "force", "f", "hard", "delete", "d", "recursive", "r",
			"force-push", "ignore-errors", "no-preserve-root":
			return true
		}

		if strings.Contains(tok, ":..") || strings.HasPrefix(tok, ":") {
			return true
		}
	}
	return false
}

func MakeKey(toolName string, input json.RawMessage) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return toolName, true
	}

	for _, key := range []string{"file_path", "path", "directory", "notebook_path"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return toolName + ":" + pathSignature(s), true
			}
		}
	}

	if v, ok := m["files"]; ok {
		var files []map[string]json.RawMessage
		if json.Unmarshal(v, &files) == nil && len(files) > 0 {
			sigs := make([]string, 0, len(files))
			for _, f := range files {
				if rawPath, ok := f["path"]; ok {
					var s string
					if json.Unmarshal(rawPath, &s) == nil && s != "" {
						sigs = append(sigs, pathSignature(s))
					}
				}
			}
			if len(sigs) > 0 {
				sort.Strings(sigs)
				return toolName + ":" + strings.Join(sigs, ","), true
			}
		}
	}

	for _, key := range []string{"url"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return toolName + ":" + urlSignature(s), true
			}
		}
	}

	for _, key := range []string{"prompt", "task", "message", "query"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return toolName + ":" + bodySignature(s), true
			}
		}
	}

	for _, key := range []string{"command", "input"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return toolName + ":" + commandSignature(s), true
			}
		}
	}

	return toolName, true
}

func bodySignature(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func urlSignature(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return pathSignature(raw)
	}
	sig := u.Scheme + "://" + u.Host
	if u.Path != "" && u.Path != "/" {
		segs := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)
		if segs[0] != "" {
			sig += "/" + segs[0]
		}
	}
	return sig
}

func (am *ApprovalMemory) EnsureModeScope(mode PermissionMode) {
	if am == nil {
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.lastMode != mode {
		am.lastMode = mode
		am.store = make(map[string]*approvalEntry)
	}
}

func (am *ApprovalMemory) ShouldAutoApprove(toolName string, input json.RawMessage) bool {
	if am == nil {
		return false
	}
	key, _ := MakeKey(toolName, input)
	am.mu.RLock()
	defer am.mu.RUnlock()
	entry, ok := am.store[key]
	if !ok {
		return false
	}
	return entry.autoApproved
}

func (am *ApprovalMemory) RecordApproval(toolName string, input json.RawMessage) {
	if am == nil {
		return
	}
	key, _ := MakeKey(toolName, input)
	am.mu.Lock()
	defer am.mu.Unlock()

	entry, ok := am.store[key]
	if !ok {

		if len(am.store) >= maxTrackedKeys {
			am.evictOldest()
		}
		entry = &approvalEntry{}
		am.store[key] = entry
	}
	entry.consecutive++
	if entry.consecutive >= autoApproveThreshold && !entry.autoApproved {
		entry.autoApproved = true
		debug.Log("approval-memory", "auto-approve activated for %s (after %d approvals)", key, entry.consecutive)
	}
}

func (am *ApprovalMemory) RecordDeny(toolName string, input json.RawMessage) {
	if am == nil {
		return
	}
	key, _ := MakeKey(toolName, input)
	am.mu.Lock()
	defer am.mu.Unlock()

	if entry, ok := am.store[key]; ok {
		entry.consecutive = 0
		entry.autoApproved = false
	}
}

func (am *ApprovalMemory) Reset() {
	if am == nil {
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	am.store = make(map[string]*approvalEntry)
}

func (am *ApprovalMemory) AutoApprovedKeys() []string {
	if am == nil {
		return nil
	}
	am.mu.RLock()
	defer am.mu.RUnlock()
	var keys []string
	for k, e := range am.store {
		if e.autoApproved {
			keys = append(keys, k)
		}
	}
	return keys
}

func (am *ApprovalMemory) evictOldest() {
	var minKey string
	var minCount int = -1
	for k, e := range am.store {
		if minCount == -1 || e.consecutive < minCount {
			minKey = k
			minCount = e.consecutive
		}
	}
	if minKey != "" {
		delete(am.store, minKey)
	}
}
