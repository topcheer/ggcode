package a2a

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// #5605(C): file-backed warm roster. Daemons append themselves to
// <workspace>/.ggcode/a2a-roster.json on Register (and remove on
// Unregister); a one-shot CLI with an empty mDNS window falls back to
// this file - zero-wait roster, mDNS keeps covering cross-machine peers.
//
// Schema: {"instances": [{instance fields..., "seen": RFC3339}]}. Stale
// entries (seen older than RosterTTL) are ignored by readers; writers
// prune them opportunistically.

// RosterTTL bounds how long a file entry counts without a refresh.
const RosterTTL = 10 * time.Minute

type rosterFileEntry struct {
	InstanceInfo
	Seen string `json:"seen"`
}

type rosterFile struct {
	Instances []rosterFileEntry `json:"instances"`
}

// RosterFilePath returns the roster file for a workspace dir.
func RosterFilePath(workspaceDir string) string {
	return filepath.Join(workspaceDir, ".ggcode", "a2a-roster.json")
}

// writeRosterEntry upserts info into the roster file (best effort -
// failures are the caller's debug noise, never fatal: the mDNS path
// stays primary).
func writeRosterEntry(workspaceDir string, info InstanceInfo) {
	path := RosterFilePath(workspaceDir)
	rf := readRosterFile(path)
	now := time.Now().UTC().Format(time.RFC3339)
	kept := rf.Instances[:0]
	for _, e := range rf.Instances {
		if e.ID != info.ID && freshEntry(e) {
			kept = append(kept, e)
		}
	}
	kept = append(kept, rosterFileEntry{InstanceInfo: info, Seen: now})
	rf.Instances = kept
	b, err := json.MarshalIndent(rf, "", " ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o644)
}

// removeRosterEntry drops info.ID from the roster file.
func removeRosterEntry(workspaceDir, id string) {
	path := RosterFilePath(workspaceDir)
	rf := readRosterFile(path)
	kept := rf.Instances[:0]
	for _, e := range rf.Instances {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	rf.Instances = kept
	b, err := json.MarshalIndent(rf, "", " ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}

// ReadRoster returns the fresh, non-self entries of the workspace's
// roster file. Missing file yields nil.
func ReadRoster(workspaceDir, selfID string) []InstanceInfo {
	rf := readRosterFile(RosterFilePath(workspaceDir))
	var out []InstanceInfo
	for _, e := range rf.Instances {
		if e.ID != selfID && freshEntry(e) && e.Endpoint != "" {
			out = append(out, e.InstanceInfo)
		}
	}
	return out
}

func freshEntry(e rosterFileEntry) bool {
	t, err := time.Parse(time.RFC3339, e.Seen)
	if err != nil {
		return false
	}
	return time.Since(t) < RosterTTL
}

func readRosterFile(path string) rosterFile {
	var rf rosterFile
	b, err := os.ReadFile(path)
	if err != nil {
		return rf
	}
	_ = json.Unmarshal(b, &rf)
	return rf
}

var rosterWriteMu sync.Mutex

// RosterUpsert is the exported write used by Register wiring.
func RosterUpsert(workspaceDir string, info InstanceInfo) {
	rosterWriteMu.Lock()
	defer rosterWriteMu.Unlock()
	writeRosterEntry(workspaceDir, info)
}

// RosterRemove is the exported remove used by Unregister wiring.
func RosterRemove(workspaceDir, id string) {
	rosterWriteMu.Lock()
	defer rosterWriteMu.Unlock()
	removeRosterEntry(workspaceDir, id)
}
