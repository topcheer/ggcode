package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// #4363 companion tests.
//
// (1) Corrupt board quarantine: RestoreJSON failure must rename the
// undecodable board to board.json.corrupt-<ts> BEFORE returning an empty
// board. Without the rename, the next board change would atomically
// overwrite the file (persistTeamBoardNow temp+rename) and the team's
// entire task history would be unrecoverable.
func TestZZBoardCorruptQuarantine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	teamID := "quarantine-team"
	path := boardPersistPath(teamID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := "definitely-not-a-board-payload"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed corrupt board: %v", err)
	}

	tm := loadTeamBoard(teamID)
	if tm == nil {
		t.Fatal("loadTeamBoard must return a usable empty manager")
	}
	if got := len(tm.List()); got != 0 {
		t.Fatalf("corrupt board must load as empty, got %d tasks", got)
	}
	// The original file must be GONE from the live path: the lazy overwrite
	// chain (EmitBoardUpdated -> persistTeamBoardNow) would otherwise
	// destroy it on the first board change.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt board must be renamed away from live path, stat err=%v", err)
	}
	// And preserved verbatim in a quarantine file.
	quarantined, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(quarantined) != 1 {
		t.Fatalf("expected exactly one quarantine file, got %v (err=%v)", quarantined, err)
	}
	saved, err := os.ReadFile(quarantined[0])
	if err != nil || string(saved) != original {
		t.Fatalf("quarantine file must preserve original content verbatim, got %q (err=%v)", saved, err)
	}
}

// (2) depFindingsPrompt budget skip must not eat later small entries: an
// over-budget entry skips ITSELF (continue), not the rest of the list.
func TestZZDepFindingsSkipKeepsLaterSmallEntries(t *testing.T) {
	_, board, _ := newClaimHarness(t)
	completed := task.StatusCompleted

	var depIDs []string
	// Five near-max entries (~810 bytes each) fill most of the 4800 budget.
	for i := 0; i < 5; i++ {
		dep := board.Create("big-dep", "d", "", nil)
		if _, err := board.Update(dep.ID, task.UpdateOptions{
			Status:   &completed,
			Metadata: map[string]string{resultMetaKey: strings.Repeat("f", 780)},
		}); err != nil {
			t.Fatalf("big dep %d: %v", i, err)
		}
		depIDs = append(depIDs, dep.ID)
	}
	// A sixth near-max entry would exceed the remaining budget: it must be
	// skipped alone.
	big6 := board.Create("over-budget-dep", "d", "", nil)
	if _, err := board.Update(big6.ID, task.UpdateOptions{
		Status:   &completed,
		Metadata: map[string]string{resultMetaKey: strings.Repeat("g", 780)},
	}); err != nil {
		t.Fatalf("over-budget dep: %v", err)
	}
	depIDs = append(depIDs, big6.ID)
	// A later small entry still fits the remaining budget: with the old
	// `break` it was silently dropped; with #4363's `continue` it survives.
	tail := board.Create("tail-dep", "d", "", nil)
	if _, err := board.Update(tail.ID, task.UpdateOptions{
		Status:   &completed,
		Metadata: map[string]string{resultMetaKey: "tiny tail finding"},
	}); err != nil {
		t.Fatalf("tail dep: %v", err)
	}
	depIDs = append(depIDs, tail.ID)

	main := board.Create("claim me", "d", "", nil)
	if _, err := board.Update(main.ID, task.UpdateOptions{AddBlockedBy: depIDs}); err != nil {
		t.Fatalf("wire deps: %v", err)
	}
	tk, ok := board.Get(main.ID)
	if !ok {
		t.Fatal("main task missing")
	}

	out := depFindingsPrompt(board, tk)
	if !strings.Contains(out, "tiny tail finding") {
		t.Fatalf("later small entry must survive an over-budget skip, got:\n%.200s", out)
	}
	if strings.Contains(out, "gggggg") {
		t.Fatalf("over-budget entry itself must still be skipped")
	}
}
