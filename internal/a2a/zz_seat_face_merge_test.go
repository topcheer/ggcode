package a2a

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMergeRosterIdentities proves the introspect-list Seat-face fix: mDNS
// advertisements whose TXT was frozen before the seat identity landed get
// their nick/role/team backfilled from the file-backed warm roster, including
// the self entry (empty selfID), while already-named instances and unknown
// pids stay untouched.
func TestMergeRosterIdentities(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ggcode")

	reg := &Registry{selfInfo: &InstanceInfo{ID: "self-1", PID: 100, Workspace: ws}}

	roster := []InstanceInfo{
		{ID: "self-1", PID: 100, Workspace: ws, Endpoint: "http://127.0.0.1:1", Nick: "CalmKoala_developer_agent", Role: "developer"},
		{ID: "peer-2", PID: 200, Workspace: ws, Endpoint: "http://127.0.0.1:2", Nick: "ZenLion_developer_agent", Role: "developer"},
		{ID: "peer-3", PID: 300, Workspace: ws, Endpoint: "http://127.0.0.1:3"}, // no identity published
	}
	for _, e := range roster {
		RosterUpsert(ws, e)
	}
	t.Cleanup(func() { _ = os.Remove(RosterFilePath(ws)) })

	instances := []InstanceInfo{
		{ID: "mdns-100", PID: 100, Workspace: ws},                      // self, frozen TXT
		{ID: "mdns-200", PID: 200, Workspace: ws},                      // peer, frozen TXT
		{ID: "mdns-300", PID: 300, Workspace: ws},                      // peer, roster has no nick either
		{ID: "mdns-400", PID: 400, Workspace: ws},                      // not in roster at all
		{ID: "mdns-100x", PID: 100, Workspace: ws, Nick: "AlreadySet"}, // must not be overwritten
	}

	reg.mergeRosterIdentities(instances)

	want := map[string]string{
		"mdns-100":  "CalmKoala_developer_agent",
		"mdns-200":  "ZenLion_developer_agent",
		"mdns-300":  "",
		"mdns-400":  "",
		"mdns-100x": "AlreadySet",
	}
	for _, inst := range instances {
		if got := inst.Nick; got != want[inst.ID] {
			t.Errorf("%s: nick=%q, want %q", inst.ID, got, want[inst.ID])
		}
	}
	if instances[0].Role != "developer" {
		t.Errorf("role not merged: %q", instances[0].Role)
	}
}
