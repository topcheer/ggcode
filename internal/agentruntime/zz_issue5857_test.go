package agentruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/swarm"
)

// #5857 prompt-surface pins: the Persistent Teams system-prompt section
// must surface pre-scoping teams (no owner-session stamp) living in the
// global store with a migration hint, while strict #5853 visibility keeps
// them out of the session-filtered list. Uses only pre-existing symbols
// so the battery compiles at the pre-fix parent for red pins.

func write5857SurfaceManifest(t *testing.T, root, teamID, name, ownerSession string) {
	t.Helper()
	dir := filepath.Join(root, "swarm", "teams", teamID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mf := map[string]any{
		"teamID":    teamID,
		"name":      name,
		"leaderID":  "lead-1",
		"createdAt": time.Now().UTC().Format(time.RFC3339),
		"teammates": []map[string]any{{"id": "tm-1", "name": "worker-1"}},
	}
	if ownerSession != "" {
		mf["ownerSessionID"] = ownerSession
	}
	data, err := json.Marshal(mf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// With only an unstamped pre-scoping team on disk, the hint must name it
// and point at team_restore (the migration channel) - previously the
// section was empty and the team was undiscoverable.
func TestIssue5857_HintSurfacesMigratableGlobalTeams(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write5857SurfaceManifest(t, filepath.Join(home, ".ggcode"), "team-92530c", "et-ops", "")

	hint := buildPersistentTeamsHint()
	if !strings.Contains(hint, "et-ops") || !strings.Contains(hint, "team_restore") {
		t.Fatalf("persistent-teams hint must surface migratable pre-scoping teams, got %q", hint)
	}
	if v := swarm.SummarizeVisibleTeams("some-session"); len(v) != 0 {
		t.Fatalf("strict #5853 visibility must stay: unstamped teams remain invisible, got %+v", v)
	}
}

// With nothing migratable, the hint lists the session teams exactly as
// before and carries no migration wording.
func TestIssue5857_HintCleanWhenNothingMigratable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write5857SurfaceManifest(t, filepath.Join(home, ".ggcode"), "team-vis1", "live-team", "sess-hint-5857")
	swarm.SetProcessOwnerSession("sess-hint-5857")
	defer swarm.SetProcessOwnerSession("")

	hint := buildPersistentTeamsHint()
	if !strings.Contains(hint, "live-team") {
		t.Fatalf("session-visible team must stay listed, got %q", hint)
	}
	if strings.Contains(hint, "one-shot migration") {
		t.Fatalf("no migration hint when nothing is migratable, got %q", hint)
	}
}
