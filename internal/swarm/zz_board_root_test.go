package swarm

import (
	"strings"
	"testing"
)

// #5540 + #5829: team IDs are random per Manager (team-<6hex>), so two
// Managers can never mint the same team; combined with the BoardRoot
// override, boards are fully isolated on disk. Production default (empty
// BoardRoot) keeps the shared config-dir path.
func TestBoardRootIsolatesDeterministicTeams(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	mA := newBoardTestManager(t)
	mA.BoardRoot = rootA
	mB := newBoardTestManager(t)
	mB.BoardRoot = rootB

	teamA := mA.CreateTeam("test", "leader")
	teamB := mB.CreateTeam("test", "leader")
	if teamA.ID == teamB.ID {
		t.Fatalf("random team IDs must differ across managers, got %s twice", teamA.ID)
	}

	// Different IDs (and roots): each writes its own board file.
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
