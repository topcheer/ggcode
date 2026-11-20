package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// P2 pin: manifest round-trip (create -> serialize -> load -> equality).
func TestManifestRoundTrip(t *testing.T) {
	root := t.TempDir()
	m := NewManager(config.SwarmConfig{}, "sess-t", nil, nil, nil)
	m.BoardRoot = root
	m.SessionRoot = t.TempDir()
	m.JournalsEnabled = true

	s := m.CreateTeam("roundtrip-team", "leader-9")
	if _, err := m.SpawnTeammate(s.ID, "dave", "32", []string{"read_file", "grep"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	mf, err := m.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if mf.TeamID != s.ID || mf.Name != "roundtrip-team" || mf.LeaderID != "leader-9" {
		t.Fatalf("manifest identity mismatch: %+v", mf)
	}
	if len(mf.Teammates) != 1 {
		t.Fatalf("manifest teammates = %d, want 1", len(mf.Teammates))
	}
	mt := mf.Teammates[0]
	if mt.Name != "dave" || mt.Color != "32" {
		t.Fatalf("teammate fields mismatch: %+v", mt)
	}
	if len(mt.AllowedTools) != 2 || mt.AllowedTools[0] != "grep" || mt.AllowedTools[1] != "read_file" {
		t.Fatalf("allowedTools not sorted-persisted: %v", mt.AllowedTools)
	}
	if mt.SessionFile == "" || !strings.Contains(mt.SessionFile, "-tm-") {
		t.Fatalf("sessionFile %q lacks the -tm- journal convention", mt.SessionFile)
	}
	if !mf.CreatedAt.Equal(s.CreatedAt) {
		t.Fatalf("createdAt drifted: %v vs %v", mf.CreatedAt, s.CreatedAt)
	}

	// Field equality through a serialize/load cycle.
	raw, err := json.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back TeamManifest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.TeamID != mf.TeamID || len(back.Teammates) != len(mf.Teammates) ||
		back.Teammates[0].SessionFile != mf.Teammates[0].SessionFile {
		t.Fatalf("round-trip field mismatch: %+v vs %+v", back, mf)
	}
}

// P2 pin: concurrent manifest writes serialize through the lock family -
// the file is never left corrupt.
func TestManifestConcurrentWritesSerialized(t *testing.T) {
	root := t.TempDir()
	m := NewManager(config.SwarmConfig{}, "sess-t", nil, nil, nil)
	m.BoardRoot = root

	s := m.CreateTeam("concurrent-team", "leader")
	eve, err := m.SpawnTeammate(s.ID, "eve", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	tmID := eve.ID

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.stampTeammateResult(s.ID, tmID, fmt.Sprintf("result-%d", n), time.Now())
			if err := m.upsertManifestTeammate(s.ID, ManifestTeammate{
				ID: tmID, Name: "eve", SessionFile: "x-tm-team-1-eve", LastActiveAt: time.Now(),
			}); err != nil {
				t.Errorf("concurrent upsert: %v", err)
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(m.manifestPath(s.ID))
	if err != nil {
		t.Fatalf("manifest unreadable after concurrent writes: %v", err)
	}
	var mf TeamManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		t.Fatalf("manifest corrupt after concurrent writes: %v", err)
	}
}

// P2 pin: DeleteTeam removes the manifest (and the whole team dir).
func TestDeleteTeamRemovesManifest(t *testing.T) {
	root := t.TempDir()
	m := NewManager(config.SwarmConfig{}, "sess-t", nil, nil, nil)
	m.BoardRoot = root

	s := m.CreateTeam("doomed", "leader")
	if _, err := m.SpawnTeammate(s.ID, "frank", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := os.Stat(m.manifestPath(s.ID)); err != nil {
		t.Fatalf("manifest should exist before delete: %v", err)
	}
	if err := m.DeleteTeam(s.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(m.manifestPath(s.ID)); !os.IsNotExist(err) {
		t.Fatalf("manifest should be gone after delete, got err=%v", err)
	}
	if _, err := os.Stat(m.teamDir(s.ID)); !os.IsNotExist(err) {
		t.Fatalf("team dir should be gone after delete, got err=%v", err)
	}
}
