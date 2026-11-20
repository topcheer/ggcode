package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// Prompt-facing discoverability for persistent teams (#5829 follow-up).
//
// Design intent: a persistent team should be "user mentions it, the agent
// pulls it up". That requires the agent to KNOW the team exists after a
// restart — before the user mentions it. Without this surface, a fresh
// instance starts with an empty teammate_list and nothing in its prompt
// points at the on-disk teams, so the feature is effectively invisible
// until someone already knows the tool name.

// PersistedTeamInfo is a prompt-facing summary of one persisted team.
type PersistedTeamInfo struct {
	TeamID     string
	Name       string
	LeaderID   string
	Members    int
	LastActive time.Time
}

// SummarizePersistedTeams scans <configDir>/swarm/teams/*/manifest.json
// and returns one summary per persisted team, newest activity first.
// Uses util.ConfigDir() (the default Manager boardRoot) so the scan lands
// on the same directory the Manager persists to. Note: an explicit
// Manager.BoardRoot override is not visible here (prompt building has no
// Manager); default-layout deployments are fully covered.
// Pure disk read — no Manager instance required.
func SummarizePersistedTeams() []PersistedTeamInfo {
	return SummarizeVisibleTeams(processOwnerSession())
}

// SummarizeVisibleTeams scans <configDir>/swarm/teams and keeps only the
// teams the given session may see: its own teams plus adoptable legacy
// (unclaimed) ones. Foreign-session teams are invisible (#5837). An empty
// session ID keeps the unfiltered legacy behavior.
func SummarizeVisibleTeams(sessionID string) []PersistedTeamInfo {
	// Teams persist in the GLOBAL store; scoping is purely logical
	// (#5852 user ruling): workspace+session. Strict session equality -
	// no unclaimed visibility, no empty-session passthrough.
	if sessionID == "" {
		return nil
	}
	root := util.ConfigDir()
	out := make([]PersistedTeamInfo, 0)
	for _, info := range summarizePersistedTeamsUnder(root) {
		mf, err := readManifestFile(filepath.Join(root, "swarm", "teams", info.TeamID, "manifest.json"))
		if err != nil {
			continue
		}
		if mf.OwnerSessionID == sessionID {
			out = append(out, info)
		}
	}
	return out
}

// SummarizeMigratableTeams scans the GLOBAL store for pre-scoping teams:
// persisted manifests with no owner-session stamp (#5857). Such teams are
// real data created before stamps existed; the strict session filter keeps
// them invisible and adoption only fires on an explicit team_restore, so
// this summary feeds the prompt hint telling the user (and the agent)
// which old teams can still be migrated. Foreign-stamped teams are never
// reported: they belong to another session and stay invisible (#5853).
func SummarizeMigratableTeams() []PersistedTeamInfo {
	root := util.ConfigDir()
	out := make([]PersistedTeamInfo, 0)
	for _, info := range summarizePersistedTeamsUnder(root) {
		mf, err := readManifestFile(filepath.Join(root, "swarm", "teams", info.TeamID, "manifest.json"))
		if err != nil {
			continue
		}
		if mf.OwnerSessionID == "" {
			out = append(out, info)
		}
	}
	return out
}

// summarizePersistedTeamsUnder scans <root>/swarm/teams (test entry for
// arbitrary board roots).
func summarizePersistedTeamsUnder(root string) []PersistedTeamInfo {
	teamsDir := filepath.Join(root, "swarm", "teams")
	entries, err := os.ReadDir(teamsDir)
	if err != nil {
		return nil // no teams dir yet — nothing to surface
	}
	var infos []PersistedTeamInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mf, err := readManifestFile(filepath.Join(teamsDir, e.Name(), "manifest.json"))
		if err != nil {
			debug.Log("swarm", "prompt surface: manifest of %s unreadable: %v", e.Name(), err)
			continue
		}
		info := PersistedTeamInfo{
			TeamID:   mf.TeamID,
			Name:     mf.Name,
			LeaderID: mf.LeaderID,
			Members:  len(mf.Teammates),
		}
		for i := range mf.Teammates {
			if mf.Teammates[i].LastActiveAt.After(info.LastActive) {
				info.LastActive = mf.Teammates[i].LastActiveAt
			}
		}
		if info.LastActive.IsZero() {
			info.LastActive = mf.CreatedAt
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].LastActive.After(infos[j].LastActive) })
	return infos
}

// readManifestFile parses one manifest.json (same semantics as
// Manager.loadManifest, but usable without a Manager instance).
func readManifestFile(path string) (*TeamManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var mf TeamManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("manifest %s corrupt: %w", path, err)
	}
	return &mf, nil
}
