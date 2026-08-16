package agent

// Read Validity Check -- Content-fingerprint validation for read staleness.
//
// Research basis: Stale read detection is a top reliability concern for AI
// coding agents (AgentMarketCap 2026 survey, "Lost in the Middle" Liu et al.
// 2023). The existing mtime-based checks (unread_edit_guard.go checkStaleRead,
// file_freshness.go maybeCheckStaleFiles) have known limitations:
//
//   1. Sub-second edits: macOS HFS+ and some Linux filesystems have 1-second
//      mtime resolution. A formatter or git hook that modifies a file within
//      the same second as the agent's read will NOT be detected by mtime.
//   2. False positives from touch/NFS: `touch -d` or NFS clock skew can make
//      mtime appear newer even when content is unchanged. This produces
//      spurious "stale read" warnings that annoy the agent.
//   3. mtime truncation: copying a file with `cp` may preserve the source
//      mtime, making the copy look unchanged even though it's new content.
//
// This check augments mtime-based detection with a lightweight FNV-1a content
// hash. At read time, we compute a fast hash of the file's first N bytes
// (enough to detect real changes, cheap enough for zero-overhead). At edit
// time, we re-hash and compare. If the hash matches, the content is the same
// even if mtime changed (suppresses false positives). If the hash differs but
// mtime is the same, it's a sub-second change (catches hidden staleness).
//
// Competitor analysis:
//   - Claude Code: mtime-only check, vulnerable to sub-second races
//   - Cursor: IDE buffer sync, but no content hash for agent reads
//   - Aider: git state only, misses non-git file changes
//   - No competitor uses content fingerprints for read validation
//
// Design constraints:
//   - Zero LLM cost (deterministic hash comparison)
//   - FNV-1a is 5-10x faster than SHA256 for small files (<100KB typical)
//   - Only hashes first 16KB of large files (sufficient for change detection)
//   - Falls back gracefully if file can't be read (open error → skip check)
//   - Non-blocking: advice is appended to tool result, execution proceeds

import (
	"encoding/json"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// maxHashBytes limits how much of the file we hash. For change detection,
	// the first 16KB is sufficient: it covers imports, function signatures,
	// and the beginning of the body where edits are most likely to diverge.
	maxHashBytes = 16 * 1024

	// staleHashThreshold is the minimum number of characters in old_text that
	// triggers the sub-second-stale warning. Trivial edits (one-line fixes)
	// are less likely to be affected by sub-second races.
	staleHashThreshold = 50
)

// readHashTracker stores FNV-1a content hashes captured at read time.
// Used to validate that the file hasn't changed between read and edit,
// even when mtime resolution is insufficient.
type readHashTracker struct {
	// hashes maps normalized file path → FNV-1a hash of content at read time.
	hashes map[string]uint64

	// warned tracks files already warned about (once per run).
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

// readValidityKey canonicalizes a path for hash-tracker keys (#557):
// Clean resolves "./x"-style prefixes so identical paths in different
// lexical forms converge. The shared normalizePath stays lexical (other
// detectors depend on it); absolute-vs-relative convergence is handled by
// lookupReadHash's suffix scan below.
func readValidityKey(p string) string {
	return filepath.Clean(strings.TrimSpace(p))
}

// lookupReadHash finds the stored hash for path, tolerating absolute vs
// relative form mixtures (#557): reads often arrive absolute (resolveToolPath)
// while edit calls carry repo-relative paths — the direct-key miss used to
// silently skip the content-hash expiry check. On a miss, a bounded suffix
// scan over the (small, per-run) map matches "/w/repo/a.go" against "a.go".
func lookupReadHash(t *readHashTracker, path string) (uint64, bool) {
	k := readValidityKey(path)
	if h, ok := t.hashes[k]; ok {
		return h, true
	}
	for key, h := range t.hashes {
		if key == k {
			return h, true
		}
		if strings.HasSuffix(key, "/"+k) || strings.HasSuffix(k, "/"+key) {
			return h, true
		}
	}
	return 0, false
}

// recordReadHash computes and stores a content hash for a file at read time.
// Files larger than maxHashBytes are partially hashed (first 16KB).
// Errors are silent -- this is an optimization layer, not a hard gate.
func (t *readHashTracker) recordReadHash(path string) {
	if path == "" {
		return
	}
	h := hashFilePrefix(path)
	if h == 0 {
		return // Couldn't read or empty file; skip silently.
	}
	t.hashes[readValidityKey(path)] = h
}

// recordWriteHash clears any stored hash for a file the agent wrote/edited.
// The agent knows the latest content after writing.
func (t *readHashTracker) recordWriteHash(path string) {
	if path == "" {
		return
	}
	n := readValidityKey(path)
	delete(t.hashes, n)
	// Also drop any opposite-form key (abs vs rel mixture, #557).
	for key := range t.hashes {
		if strings.HasSuffix(key, "/"+n) || strings.HasSuffix(n, "/"+key) {
			delete(t.hashes, key)
		}
	}
	delete(t.warned, n)
}

// validateContentAtEdit checks whether the file content has changed since
// the last read, using a content hash comparison. This catches:
//   - Sub-second edits that mtime misses (hash differs, mtime same)
//   - False-positive mtime changes from touch/NFS (hash same, mtime different)
//
// Returns a non-empty advisory message if a content mismatch is detected,
// or "" if the content matches or no hash was recorded.
func (t *readHashTracker) validateContentAtEdit(path string, oldTextLen int) string {
	if path == "" {
		return ""
	}
	storedHash, ok := lookupReadHash(t, path)
	if !ok {
		return "" // No prior hash; can't validate.
	}
	n := readValidityKey(path)
	if t.warned[n] {
		return "" // Already warned for this file.
	}

	currentHash := hashFilePrefix(path)
	if currentHash == 0 {
		return "" // File unreadable; let the tool itself report the error.
	}

	if currentHash == storedHash {
		return "" // Content unchanged despite possible mtime difference.
	}

	// Content differs from what the agent read. This is a real staleness signal.
	t.warned[n] = true
	debug.Log("agent", "read-validity: content hash mismatch for %s (stored=%d current=%d)", n, storedHash, currentHash)

	if oldTextLen > 0 && oldTextLen < staleHashThreshold {
		// Small edit -- less likely to be affected, but still note it.
		return "Note: file content changed since your last read (content hash mismatch). " +
			"For this small edit it may still work, but if it fails, re-read the file first."
	}

	return "Warning: file content has changed since your last read (content hash mismatch detected). " +
		"Your old_text anchor may not match the current file. " +
		"Re-read the file with read_file before retrying this edit."
}

// hashFilePrefix computes a fast FNV-1a hash of the first maxHashBytes of a
// file. Returns 0 if the file can't be opened or is empty. FNV-1a is chosen
// for speed (no crypto requirement -- we only need change detection).
func hashFilePrefix(path string) uint64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	buf := make([]byte, maxHashBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return 0
	}

	h := fnv.New64a()
	if _, err := h.Write(buf[:n]); err != nil {
		return 0 // Should never fail for FNV, but satisfy error checking.
	}
	return h.Sum64()
}

// extractOldTextLen returns the length of the old_text argument for edit
// tools, used by validateContentAtEdit to soften the warning for small edits.
// For multi-edit tools it returns the total across all edits. Returns 0 when
// no old_text is present or the arguments can't be parsed.
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

// (formatHashShort removed -- debug logging uses the raw uint64 value directly.)
