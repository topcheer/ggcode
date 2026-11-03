package agent

import (
	"encoding/json"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxHashBytes = 1024 * 1024

	staleHashThreshold = 50
)

type readHashTracker struct {
	hashes map[string]uint64

	warned map[string]bool
}

func newReadHashTracker() *readHashTracker {
	return &readHashTracker{
		hashes: make(map[string]uint64),
		warned: make(map[string]bool),
	}
}

func (t *readHashTracker) reset() {
	t.hashes = make(map[string]uint64)
	t.warned = make(map[string]bool)
}

func readValidityKey(p string) string {
	return filepath.Clean(strings.TrimSpace(p))
}

func resolveReadHashKey(t *readHashTracker, path string) (string, bool) {
	k := readValidityKey(path)
	if _, ok := t.hashes[k]; ok {
		return k, true
	}
	match := ""
	count := 0
	for key := range t.hashes {
		if strings.HasSuffix(key, "/"+k) || strings.HasSuffix(k, "/"+key) {
			match = key
			count++
		}
	}
	if count == 1 {
		return match, true
	}
	if count > 1 {
		debug.Log("agent", "read-validity: ambiguous suffix match for %s (%d same-basename candidates), skipping lookup", k, count)
	}
	return "", false
}

func lookupReadHash(t *readHashTracker, path string) (uint64, bool) {
	if key, ok := resolveReadHashKey(t, path); ok {
		return t.hashes[key], true
	}
	return 0, false
}

func (t *readHashTracker) recordReadHash(path string) {
	if path == "" {
		return
	}
	h := hashFilePrefix(path)
	if h == 0 {
		return
	}
	t.hashes[readValidityKey(path)] = h
}

func (t *readHashTracker) recordWriteHash(path string) {
	if path == "" {
		return
	}
	n := readValidityKey(path)
	delete(t.hashes, n)

	matched := make([]string, 0, 2)
	for key := range t.hashes {
		if key == n {
			continue
		}
		if strings.HasSuffix(n, "/"+key) || strings.HasSuffix(key, "/"+n) {
			matched = append(matched, key)
		}
	}
	if len(matched) == 1 {
		delete(t.hashes, matched[0])
	}
	delete(t.warned, n)
	if wk, ok := resolveWarnedKey(t, path); ok {
		delete(t.warned, wk)
	}
}

func resolveWarnedKey(t *readHashTracker, path string) (string, bool) {
	k := readValidityKey(path)
	match := ""
	count := 0
	for key := range t.warned {
		if key == k || strings.HasSuffix(key, "/"+k) || strings.HasSuffix(k, "/"+key) {
			match = key
			count++
		}
	}
	if count == 1 {
		return match, true
	}
	return "", false
}

func (t *readHashTracker) validateContentAtEdit(path string, oldTextLen int) string {
	if path == "" {
		return ""
	}
	storedHash, ok := lookupReadHash(t, path)
	if !ok {
		return ""
	}
	n := readValidityKey(path)
	if t.warned[n] {
		return ""
	}

	currentHash := hashFilePrefix(path)
	if currentHash == 0 {
		return ""
	}

	if currentHash == storedHash {
		return ""
	}

	t.warned[n] = true
	debug.Log("agent", "read-validity: content hash mismatch for %s (stored=%d current=%d)", n, storedHash, currentHash)

	if oldTextLen > 0 && oldTextLen < staleHashThreshold {

		return "Note: file content changed since your last read (content hash mismatch). " +
			"For this small edit it may still work, but if it fails, re-read the file first."
	}

	return "Warning: file content has changed since your last read (content hash mismatch detected). " +
		"Your old_text anchor may not match the current file. " +
		"Re-read the file with read_file before retrying this edit."
}

func hashFilePrefix(path string) uint64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	size := maxHashBytes
	if fi, err := f.Stat(); err == nil && fi.Size() < int64(size) {
		size = int(fi.Size())
	}
	if size <= 0 {
		return 0
	}

	buf := make([]byte, size)
	n, err := io.ReadFull(f, buf)
	if err != nil && n == 0 {
		return 0
	}

	h := fnv.New64a()
	if _, err := h.Write(buf[:n]); err != nil {
		return 0
	}
	return h.Sum64()
}

func extractOldTextLen(toolName string, args json.RawMessage) int {
	if len(args) == 0 {
		return 0
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return 0
	}
	switch toolName {
	case "edit_file":
		if s, ok := m["old_text"].(string); ok {
			return len(s)
		}
		return 0
	case "multi_edit_file":
		editList, ok := m["edits"].([]any)
		if !ok {
			return 0
		}
		total := 0
		for _, e := range editList {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := em["old_text"].(string); ok {
				total += len(s)
			}
		}
		return total
	case "multi_file_edit":
		files, ok := m["files"].([]any)
		if !ok {
			return 0
		}
		total := 0
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			for _, edits := range fm {
				editList, ok := edits.([]any)
				if !ok {
					continue
				}
				for _, e := range editList {
					em, ok := e.(map[string]any)
					if !ok {
						continue
					}
					if s, ok := em["old_text"].(string); ok {
						total += len(s)
					}
				}
			}
		}
		return total
	}
	return 0
}
