package runfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type PortFile struct {
	Addr      string `json:"addr"`
	Token     string `json:"token"`
	PID       int    `json:"pid"`
	SessionID string `json:"session_id"`
	Workspace string `json:"workspace"`
	Mode      string `json:"mode"`

	StartTime string `json:"start_time,omitempty"`
}

const runDir = "run"

const staleAfter = 24 * time.Hour

func path(sessionID string) string {
	home := homeDir()
	if home == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode", runDir, sessionID+".json")
}

func Write(pf PortFile) error {
	if pf.SessionID == "" {
		return fmt.Errorf("session ID is required")
	}

	if pf.PID > 0 && pf.StartTime == "" {
		pf.StartTime = procStartTime(pf.PID)
	}
	p := path(pf.SessionID)
	if p == "" {
		return fmt.Errorf("cannot resolve port file path")
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		debug.Log("runfile", "Write: failed to create run dir: %v", err)
		return fmt.Errorf("create run dir: %w", err)
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal port file: %w", err)
	}

	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		debug.Log("runfile", "Write: failed to write port file for session %s: %v", pf.SessionID, err)
		return fmt.Errorf("write port file: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		debug.Log("runfile", "Write: failed to rename port file for session %s: %v", pf.SessionID, err)
		return err
	}
	debug.Log("runfile", "wrote port file for session %s (pid=%d addr=%s)", pf.SessionID, pf.PID, pf.Addr)
	return nil
}

func Remove(sessionID string) {
	p := path(sessionID)
	if p == "" {
		return
	}
	_ = os.Remove(p)
}

func Read(sessionID string) (*PortFile, error) {
	return readAtPath(path(sessionID))
}

func ReadForWorkspace(workspace string) ([]PortFile, error) {
	all, err := ReadAll()
	if err != nil {
		return nil, err
	}
	normalized := normalizeWorkspace(workspace)
	var result []PortFile
	for _, pf := range all {
		if normalizeWorkspace(pf.Workspace) == normalized {
			result = append(result, pf)
		}
	}
	return result, nil
}

func ReadAll() ([]PortFile, error) {
	home := homeDir()
	if home == "" {
		return nil, fmt.Errorf("cannot resolve home directory")
	}
	dir := filepath.Join(home, ".ggcode", runDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var result []PortFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		pf, err := readAtPath(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		result = append(result, *pf)
	}
	return result, nil
}

func readAtPath(p string) (*PortFile, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var pf PortFile
	if err := json.Unmarshal(data, &pf); err != nil {
		debug.Log("runfile", "readAtPath: failed to parse port file %s: %v", p, err)
		return nil, fmt.Errorf("parse port file: %w", err)
	}

	if pf.SessionID == "" {
		_ = os.Remove(p)
		debug.Log("runfile", "readAtPath: removed legacy port file without session_id: %s", p)
		return nil, fmt.Errorf("legacy port file without session_id, removed")
	}

	if pf.StartTime != "" {
		if now := procStartTime(pf.PID); now != "" && now != pf.StartTime {
			_ = os.Remove(p)
			debug.Log("runfile", "readAtPath: pid %d was recycled (start time %s -> %s), removed ghost port file: %s", pf.PID, pf.StartTime, now, p)
			return nil, fmt.Errorf("process %d is not the port file owner (pid recycled, removed)", pf.PID)
		}
	}
	if !isAlive(pf.PID) {
		_ = os.Remove(p)
		debug.Log("runfile", "readAtPath: removed stale port file for dead pid %d: %s", pf.PID, p)
		return nil, fmt.Errorf("process %d is not running (stale port file, removed)", pf.PID)
	}

	if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > staleAfter {
		debug.Log("runfile", "readAtPath: port file for pid %d is older than %v but pid is alive (no heartbeat; kept): %s", pf.PID, staleAfter, p)
	}
	return &pf, nil
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func normalizeWorkspace(workspace string) string {
	trimmed := strings.TrimSpace(workspace)
	if trimmed == "" {
		return ""
	}
	resolved := trimmed
	if r, err := filepath.EvalSymlinks(trimmed); err == nil {
		resolved = r
	}
	resolved = filepath.Clean(resolved)

	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(resolved)
	}
	return resolved
}

func isAlive(pid int) bool {
	return processExists(pid)
}
