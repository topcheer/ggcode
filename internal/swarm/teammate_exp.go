package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// r457: teammate experience distillation channel (swarm -> traj_intel).
//
// teammate_results is latest-only by design (documented on the tool): each
// new task overwrites the previous result, and a shutdown teammate's
// accumulated experience is lost entirely. traj_intel learned only from
// the MAIN agent's trajectory - swarm teammates (often stronger models on
// delegated tasks) contributed zero learnings back. This file is the
// write side of the fix: every completed teammate result is appended to a
// rolling JSONL ledger that traj_intel ingests as "teammate" learnings,
// so multi-task experience accumulates across runs instead of being
// overwritten.

// teammateExperienceEntry is one ledger record.
type teammateExperienceEntry struct {
	Ts       time.Time `json:"ts"`
	TeamID   string    `json:"team_id"`
	Teammate string    `json:"teammate"` // id + name for readability
	Digest   string    `json:"digest"`   // result, truncated
}

// maxTeammateExpEntries bounds the ledger (rolling window).
const maxTeammateExpEntries = 200

// teammateExpDigestLen caps the stored result text.
const teammateExpDigestLen = 500

// teammateExpPath returns the ledger location under the workspace's
// .ggcode directory.
func teammateExpPath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", "teammate-experience.jsonl")
}

// appendTeammateExperience appends one entry to the rolling ledger. Best
// effort: ledger failures must never disturb the result-store path that
// feeds teammate_results. Over-capacity ledgers are trimmed from the
// head (oldest first) by rewrite.
func appendTeammateExperience(workingDir, teamID, tmID, tmName, result string) {
	if workingDir == "" || strings.TrimSpace(result) == "" {
		return
	}
	entry := teammateExperienceEntry{
		Ts:       time.Now(),
		TeamID:   teamID,
		Teammate: tmID,
		Digest:   truncateRunes(result, teammateExpDigestLen),
	}
	if tmName != "" && tmName != tmID {
		entry.Teammate = tmID + " (" + tmName + ")"
	}
	path := teammateExpPath(workingDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		debug.Log("swarm", "teammate-exp: mkdir failed: %v", err)
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	// Absent ledger (first write) or unreadable both mean: start fresh.
	// #3260: the whole read→rewrite→rename window runs under the
	// cross-process lock - two ggcode instances sharing this workspace
	// previously both loaded the same baseline and the LAST rename won,
	// silently erasing the other's entry (same lost-update class #1512-C
	// fixed for traj_intel). Ledger stays best-effort: on lock timeout we
	// skip rather than block the event loop.
	unlock, lockErr := lockTeammateExpFile(path + ".lock")
	if lockErr != nil {
		debug.Log("swarm", "teammate-exp: lock timeout, skipping append: %v", lockErr)
		return
	}
	defer unlock()
	data, _ := os.ReadFile(path)
	payload := append(data, append(line, '\n')...)
	if len(payload) == 0 {
		return
	}
	// Trim to the rolling window: drop oldest whole lines past the cap.
	lines := splitJSONL(payload)
	if len(lines) > maxTeammateExpEntries {
		lines = lines[len(lines)-maxTeammateExpEntries:]
		payload = nil
		for _, l := range lines {
			payload = append(payload, l...)
			payload = append(payload, '\n')
		}
	}
	// #3260: unique temp file (os.CreateTemp) - the fixed ".tmp" path let
	// two concurrent writers interleave content into the same file, and
	// the reader's splitJSONL then dropped the corrupted lines.
	tmpF, err := os.CreateTemp(filepath.Dir(path), ".tmexp-*.tmp")
	if err != nil {
		debug.Log("swarm", "teammate-exp: write failed: %v", err)
		return
	}
	tmp := tmpF.Name()
	if _, err := tmpF.Write(payload); err != nil {
		tmpF.Close()
		os.Remove(tmp)
		debug.Log("swarm", "teammate-exp: write failed: %v", err)
		return
	}
	if err := tmpF.Close(); err != nil {
		os.Remove(tmp)
		debug.Log("swarm", "teammate-exp: write failed: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		debug.Log("swarm", "teammate-exp: rename failed: %v", err)
	}
}

// splitJSONL splits non-empty JSONL lines, tolerating a missing trailing
// newline (partial last line from a crashed writer is dropped).
func splitJSONL(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	return out // trailing partial line intentionally dropped
}

// truncateRunes caps a string by runes without splitting one.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// workingDirForExp resolves the manager's working dir under m.mu.
func (m *Manager) workingDirForExp() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workingDir
}
