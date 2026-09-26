package config

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sync"
	"time"
)

// Self-write attribution for the agentruntime config hot-reload watcher.
//
// Every config-family file this process persists goes through
// writeSecureConfigFile. Recording the exact bytes written lets the watcher
// distinguish "the session saved its own config" from "someone else edited
// the file". Same-process saves never need a reload cycle: the writer (TUI
// panel, /config command, WebUI, agent loop) mutates the shared in-memory
// *Config in place BEFORE persisting, so a reload would just re-merge
// identical values. Recognizing these saves keeps the watcher's signal
// reserved for genuine external edits (another instance, a manual editor)
// and avoids a redundant config.Load plus in-place merge on every save.
//
// Cross-instance propagation is unaffected: the registry is per-process, so
// another instance's saves carry no mark here and still trigger a reload.
//
// Marks are keyed by absolute path and match only the exact content hash, so
// any later edit to the same file (even one byte) fails the match and
// reloads normally.

type selfWriteMark struct {
	hash string
	at   time.Time
}

// selfWriteMarkTTL bounds how long a mark stays eligible. The watcher polls
// every 2s; 90s covers long poll stalls many times over while keeping stale
// marks from masking genuine edits that happen to byte-match an old save.
const selfWriteMarkTTL = 90 * time.Second

var (
	selfWriteMarksMu sync.Mutex
	selfWriteMarks   = make(map[string]selfWriteMark)
)

// NoteConfigSelfWrite records the bytes this process just persisted to path.
// Called by writeSecureConfigFile after a successful atomic write; unwatched
// files (im.yaml, keys.env, instance files) accumulate harmless marks that
// expire via the TTL prune.
func NoteConfigSelfWrite(path string, data []byte) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	sum := sha256.Sum256(data)
	now := time.Now()
	selfWriteMarksMu.Lock()
	// Prune expired marks so the map stays bounded on long sessions.
	for p, m := range selfWriteMarks {
		if now.Sub(m.at) > selfWriteMarkTTL {
			delete(selfWriteMarks, p)
		}
	}
	selfWriteMarks[abs] = selfWriteMark{hash: hex.EncodeToString(sum[:]), at: now}
	selfWriteMarksMu.Unlock()
}

// MatchesRecentSelfWrite reports whether path currently holds the exact
// bytes this process persisted within the TTL window. Read-only: the
// watcher may see the same mark across consecutive polls until its baseline
// catches up, and a non-consuming match keeps that ordering safe.
func MatchesRecentSelfWrite(path, contentHash string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	selfWriteMarksMu.Lock()
	m, ok := selfWriteMarks[abs]
	selfWriteMarksMu.Unlock()
	return ok && time.Since(m.at) <= selfWriteMarkTTL && m.hash == contentHash
}
