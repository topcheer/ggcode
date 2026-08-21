// Package runfile manages JSON port files at ~/.ggcode/run/<sessionID>.json
// that allow external processes to discover and query running ggcode instances.
//
// Each port file is keyed by session ID (not workspace), so multiple instances
// in the same workspace each get their own file. The port file contains the
// WebUI listen address, auth token, PID, session ID, and workspace.
//
// External tools read these files, then query the WebUI's /api/status endpoint:
//
//	curl -H "Authorization: Bearer <token>" http://<addr>/api/status
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

// PortFile is the JSON structure written to disk.
type PortFile struct {
	Addr      string `json:"addr"`       // WebUI listen address (e.g. "127.0.0.1:54321")
	Token     string `json:"token"`      // Bearer auth token for WebUI API
	PID       int    `json:"pid"`        // OS process ID
	SessionID string `json:"session_id"` // ggcode session ID
	Workspace string `json:"workspace"`  // working directory
	Mode      string `json:"mode"`       // startup permission mode (supervised, auto, etc.)
}

// runDir is the subdirectory under ~/.ggcode/ for port files.
const runDir = "run"

// staleAfter is the mtime fallback: a port file whose modification time is
// older than this is treated as stale even if its PID happens to be alive,
// because a live-looking PID may be an unrelated recycled process (issue
// #549 bug F). Port files are rewritten on every session start, so an old
// mtime cannot belong to a currently running instance.
const staleAfter = 24 * time.Hour

// path returns the port file path for the given session ID.
// Format: ~/.ggcode/run/<sessionID>.json
func path(sessionID string) string {
	home := homeDir()
	if home == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode", runDir, sessionID+".json")
}

// Write creates or overwrites the port file for the given session atomically.
func Write(pf PortFile) error {
	if pf.SessionID == "" {
		return fmt.Errorf("session ID is required")
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
	// Write to temp file then rename for atomicity.
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

// Remove deletes the port file for the given session ID if it exists.
func Remove(sessionID string) {
	p := path(sessionID)
	if p == "" {
		return
	}
	_ = os.Remove(p)
}

// Read reads and parses the port file for the given session ID.
// Returns an error if the file doesn't exist or is stale (PID not alive).
func Read(sessionID string) (*PortFile, error) {
	return readAtPath(path(sessionID))
}

// ReadForWorkspace reads all non-stale port files matching the given workspace.
// Returns all instances running in that workspace.
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

// ReadAll reads all non-stale port files in ~/.ggcode/run/.
// Useful for listing all running instances.
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
			continue // skip unreadable/stale files
		}
		result = append(result, *pf)
	}
	return result, nil
}

// readAtPath reads a port file at the given path and validates liveness.
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
	// Auto-clean legacy port files that lack session_id (old workspace-hash format)
	if pf.SessionID == "" {
		_ = os.Remove(p)
		debug.Log("runfile", "readAtPath: removed legacy port file without session_id: %s", p)
		return nil, fmt.Errorf("legacy port file without session_id, removed")
	}
	if !isAlive(pf.PID) {
		_ = os.Remove(p)
		debug.Log("runfile", "readAtPath: removed stale port file for dead pid %d: %s", pf.PID, p)
		return nil, fmt.Errorf("process %d is not running (stale port file, removed)", pf.PID)
	}
	// Mtime fallback for PID reuse: signal-0 liveness cannot distinguish a
	// recycled PID from the original owner. #791: the old rule DELETED any
	// file older than 24h, but port files are written only at process start
	// (no heartbeat), so a healthy daemon past 24h was wiped from discovery
	// while still running. Now a stale mtime with a LIVE pid only logs a
	// warning -- deletion happens exclusively in the dead-pid branch above,
	// which is the only case where recycling is certain.
	if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > staleAfter {
		debug.Log("runfile", "readAtPath: port file for pid %d is older than %v but pid is alive (no heartbeat; kept): %s", pf.PID, staleAfter, p)
	}
	return &pf, nil
}

// homeDir returns the user home directory, tolerating errors.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// normalizeWorkspace resolves symlinks and cleans the path.
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
	// #795: case-insensitive filesystems (darwin/windows default volumes)
	// treat /tmp/Proj and /tmp/proj as the same workspace, but the string
	// comparison missed the live instance. Fold case there; Linux
	// case-sensitive volumes keep exact spelling.
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(resolved)
	}
	return resolved
}

// isAlive checks whether a process with the given PID exists.
func isAlive(pid int) bool {
	return processExists(pid)
}
