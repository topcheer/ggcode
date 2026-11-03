package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type teammateExperienceEntry struct {
	Ts       time.Time `json:"ts"`
	TeamID   string    `json:"team_id"`
	Teammate string    `json:"teammate"`
	Digest   string    `json:"digest"`
}

const maxTeammateExpEntries = 200

const teammateExpDigestLen = 500

func teammateExpPath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", "teammate-experience.jsonl")
}

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

	lines := splitJSONL(payload)
	if len(lines) > maxTeammateExpEntries {
		lines = lines[len(lines)-maxTeammateExpEntries:]
		payload = nil
		for _, l := range lines {
			payload = append(payload, l...)
			payload = append(payload, '\n')
		}
	}

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
	return out
}

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

func (m *Manager) workingDirForExp() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workingDir
}
