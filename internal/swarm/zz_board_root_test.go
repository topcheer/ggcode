package swarm

import (
	"strings"
	"testing"
)

// #5540: every fresh Manager deterministically mints team-1, and the board
// persist path used to be keyed to the shared util.ConfigDir - so tests in
// the same or different packages collided on the same on-disk board.json
// ("already claimed by tm-1 in_progress on the shared board" flake). With
// the BoardRoot override, two Managers with the same team ID must be fully
// isolated on disk, and production default (empty BoardRoot) must keep the
// legacy path.
func TestBoardRootIsolatesDeterministicTeams(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	mA := newBoardTestManager(t)
	mA.BoardRoot = rootA
	mB := newBoardTestManager(t)
	mB.BoardRoot = rootB

	teamA := mA.CreateTeam("test", "leader")
	teamB := mB.CreateTeam("test", "leader")
	if teamA.ID != teamB.ID {
		t.Fatalf("both managers must mint the same deterministic team id, got %s vs %s", teamA.ID, teamB.ID)
	}

	// Same team ID, different roots: each writes its own board file.
	tmA, errA := mA.EnsureTaskManager(teamA.ID)
	if errA != nil {
		t.Fatalf("ensure A: %v", errA)
	}
	tmB, errB := mB.EnsureTaskManager(teamB.ID)
	if errB != nil {
		t.Fatalf("ensure B: %v", errB)
	}
	mA.persistTeamBoardNow(teamA.ID, tmA)
	mB.persistTeamBoardNow(teamB.ID, tmB)

	if mA.boardPersistPath(teamA.ID) == mB.boardPersistPath(teamB.ID) {
		t.Fatal("same team id under different BoardRoots must map to different paths")
	}
	if got := mA.boardPersistPath(teamA.ID); !strings.HasPrefix(got, rootA) {
		t.Fatalf("manager A board path must live under rootA, got %s", got)
	}
	if got := mB.boardPersistPath(teamB.ID); !strings.HasPrefix(got, rootB) {
		t.Fatalf("manager B board path must live under rootB, got %s", got)
	}

	// Production default: empty BoardRoot keeps the legacy config-dir path.
	mDef := newBoardTestManager(t)
	if def := mDef.boardPersistPath("team-x"); def == "" || strings.HasPrefix(def, rootA) || strings.HasPrefix(def, rootB) {
		t.Fatalf("default root must be the config dir, got %s", def)
	}
}
