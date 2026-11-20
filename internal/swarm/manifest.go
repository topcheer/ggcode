package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// Team manifest persistence (persistent teams P2).
//
// ~/.ggcode/swarm/teams/<teamID>/manifest.json describes the durable shape
// of a team: identity, membership (with each member's journal session), and
// a last-result digest that doubles as a "who did what last" dashboard.
// Written on CreateTeam / SpawnTeammate / ShutdownTeammate / DeleteTeam,
// plus a lastResultSummary stamp from the done-mail hook (#5822). All
// writes serialize through the same board lock family (board.lock) plus an
// in-process manifestMu, matching the board persist discipline.

// ManifestTeammate is one persisted member entry.
type ManifestTeammate struct {
	ID                string    `json:"id"`                     // teammate ID (board task owner identity)
	Name              string    `json:"name"`                   // display name
	Role              string    `json:"role,omitempty"`         // free-form role hint
	Color             string    `json:"color,omitempty"`        // display color
	AllowedTools      []string  `json:"allowedTools,omitempty"` // sorted; empty = full surface
	SessionFile       string    `json:"sessionFile,omitempty"`  // journal session ID (basename under sessions/)
	LastActiveAt      time.Time `json:"lastActiveAt,omitempty"`
	LastResultSummary string    `json:"lastResultSummary,omitempty"` // <=120 chars, done-mail hook
}

// TeamManifest is the persisted team descriptor.
type TeamManifest struct {
	TeamID         string             `json:"teamID"`
	Name           string             `json:"name"`
	LeaderID       string             `json:"leaderID"`
	OwnerSessionID string             `json:"ownerSessionID,omitempty"`
	CreatedAt      time.Time          `json:"createdAt"`
	UpdatedAt      time.Time          `json:"updatedAt"`
	Teammates      []ManifestTeammate `json:"teammates"`
}

func (m *Manager) manifestPath(teamID string) string {
	return filepath.Join(m.teamDir(teamID), "manifest.json")
}

// writeManifest persists the manifest under the board lock and refreshes
// the owner heartbeat (same critical section, board persist discipline).
func (m *Manager) writeManifest(teamID string, mf *TeamManifest) error {
	m.manifestMu.Lock()
	defer m.manifestMu.Unlock()
	return m.writeManifestLocked(teamID, mf)
}

func (m *Manager) writeManifestLocked(teamID string, mf *TeamManifest) error {
	if mf.TeamID == "" {
		mf.TeamID = teamID
	}
	mf.UpdatedAt = time.Now()
	if err := os.MkdirAll(m.teamDir(teamID), 0o755); err != nil {
		return err
	}
	lockPath := m.boardLockPath(teamID)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	unlock, err := util.FileLock(lockPath)
	if err != nil {
		return fmt.Errorf("manifest lock: %w", err)
	}
	defer unlock()

	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	path := m.manifestPath(teamID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	m.refreshOwnerHeartbeat(teamID)
	return nil
}

// loadManifest reads the manifest without taking the board lock (readers
// only need a point-in-time view; writes are atomic tmp+rename).
func (m *Manager) loadManifest(teamID string) (*TeamManifest, error) {
	data, err := os.ReadFile(m.manifestPath(teamID))
	if err != nil {
		return nil, err
	}
	var mf TeamManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("manifest for %s corrupt: %w", teamID, err)
	}
	return &mf, nil
}

// updateManifest performs a locked read-modify-write cycle.
func (m *Manager) updateManifest(teamID string, mutate func(*TeamManifest)) error {
	m.manifestMu.Lock()
	defer m.manifestMu.Unlock()
	mf, err := m.loadManifest(teamID)
	if err != nil {
		return err
	}
	mutate(mf)
	return m.writeManifestLocked(teamID, mf)
}

// upsertManifestTeammate adds or replaces one member entry.
func (m *Manager) upsertManifestTeammate(teamID string, mt ManifestTeammate) error {
	return m.updateManifest(teamID, func(mf *TeamManifest) {
		replaced := false
		for i := range mf.Teammates {
			if mf.Teammates[i].ID == mt.ID {
				mf.Teammates[i] = mt
				replaced = true
				break
			}
		}
		if !replaced {
			mf.Teammates = append(mf.Teammates, mt)
		}
	})
}

// removeManifestTeammate drops one member entry (ShutdownTeammate).
func (m *Manager) removeManifestTeammate(teamID, tmID string) error {
	return m.updateManifest(teamID, func(mf *TeamManifest) {
		out := mf.Teammates[:0]
		for _, mt := range mf.Teammates {
			if mt.ID != tmID {
				out = append(out, mt)
			}
		}
		mf.Teammates = out
	})
}

// stampTeammateResult records the <=120-char done-mail digest for a member
// (fired from the teammate_idle emit path — the #5822 done-summary hook).
// Skipped once the manager is shut down: a stamp landing during teardown
// would recreate the team directory and race workspace cleanup.
func (m *Manager) stampTeammateResult(teamID, tmID, result string, lastActive time.Time) {
	if m.rootCtx != nil && m.rootCtx.Err() != nil {
		return
	}
	summary := util.Truncate(firstResultLine(result), 120)
	err := m.updateManifest(teamID, func(mf *TeamManifest) {
		for i := range mf.Teammates {
			if mf.Teammates[i].ID == tmID {
				mf.Teammates[i].LastResultSummary = summary
				if !lastActive.IsZero() {
					mf.Teammates[i].LastActiveAt = lastActive
				}
				return
			}
		}
	})
	if err != nil {
		debug.Log("swarm", "manifest result stamp for %s/%s failed: %v", teamID, tmID, err)
	}
}

// findTeamManifestByName scans persisted manifests and matches on team name
// (case-insensitive) or team ID. Used by the team_restore tool.
func (m *Manager) findTeamManifestByName(name string) (*TeamManifest, error) {
	teamsDir := filepath.Join(m.boardRoot(), "swarm", "teams")
	entries, err := os.ReadDir(teamsDir)
	if err != nil {
		return nil, fmt.Errorf("no persisted teams under %s: %w", teamsDir, err)
	}
	want := equalFoldSpace(name)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mf, err := m.loadManifest(e.Name())
		if err != nil {
			continue
		}
		if equalFoldSpace(mf.Name) == want || equalFoldSpace(mf.TeamID) == want {
			return mf, nil
		}
	}
	return nil, fmt.Errorf("no persisted team matches %q", name)
}

func equalFoldSpace(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
