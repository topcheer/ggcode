package bom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zz_sa78: companion tests for the learning-artifact BOM aggregator.

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findEntry(rep *Report, typ, scope, name string) *Entry {
	for i := range rep.Entries {
		e := &rep.Entries[i]
		if e.Type == typ && e.Scope == scope && e.Name == name {
			return e
		}
	}
	return nil
}

func TestBuildAggregatesAllClasses(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()

	// Active skills: project + global.
	writeFile(t, filepath.Join(ws, ".ggcode/skills/deploy/SKILL.md"), "# deploy\n")
	writeFile(t, filepath.Join(home, ".ggcode/skills/review/SKILL.md"), "# review\n")
	// Staged skill (knight pipeline).
	writeFile(t, filepath.Join(ws, ".ggcode/skills-staging/candidate/SKILL.md"), "# candidate\n")
	// Memories: auto-saved (research-) + manual.
	writeFile(t, filepath.Join(ws, ".ggcode/memory/research-r1-impl.md"), "impl notes")
	writeFile(t, filepath.Join(ws, ".ggcode/memory/notes.md"), "hand written")
	writeFile(t, filepath.Join(home, ".ggcode/memory/release-v1.md"), "release notes")
	// Trajectory learnings ledger: 2 non-empty lines + 1 blank.
	writeFile(t, filepath.Join(ws, ".ggcode/trajectory-learnings.jsonl"), `{"insight":"a"}`+"\n"+`{"insight":"b"}`+"\n\n")
	// Disabled skill state: global skill "review" disabled.
	disabled, _ := json.Marshal([]string{"Review"}) // writer side normalizes case
	writeFile(t, filepath.Join(home, ".ggcode/disabled_skills.json"), string(disabled))

	rep, err := Build(ws, home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := rep.Counts[TypeSkill]; got != 2 {
		t.Errorf("skill count = %d, want 2", got)
	}
	if got := rep.Counts[TypeSkillStaged]; got != 1 {
		t.Errorf("staged count = %d, want 1", got)
	}
	if got := rep.Counts[TypeMemory]; got != 3 {
		t.Errorf("memory count = %d, want 3", got)
	}
	if got := rep.Counts[TypeTrajInsight]; got != 1 {
		t.Errorf("traj count = %d, want 1", got)
	}

	// Skill enabled flag honors disabled_skills.json (case-insensitive).
	if e := findEntry(rep, TypeSkill, "global", "review"); e == nil || e.Enabled {
		t.Errorf("global skill review should be disabled, got %+v", e)
	}
	if e := findEntry(rep, TypeSkill, "project", "deploy"); e == nil || !e.Enabled || e.PromotionGate != GateKnight {
		t.Errorf("project skill deploy wrong: %+v", e)
	}
	// Staged: not enabled, knight-staging gate.
	if e := findEntry(rep, TypeSkillStaged, "project", "candidate"); e == nil || e.Enabled || e.PromotionGate != GateKnightStaging {
		t.Errorf("staged skill wrong: %+v", e)
	}
	// Memory gates: research-/release- auto, notes manual.
	if e := findEntry(rep, TypeMemory, "project", "research-r1-impl"); e == nil || e.PromotionGate != GateAuto {
		t.Errorf("research memory gate wrong: %+v", e)
	}
	if e := findEntry(rep, TypeMemory, "project", "notes"); e == nil || e.PromotionGate != GateManual {
		t.Errorf("manual memory gate wrong: %+v", e)
	}
	if e := findEntry(rep, TypeMemory, "global", "release-v1"); e == nil || e.PromotionGate != GateAuto {
		t.Errorf("release memory gate wrong: %+v", e)
	}
	// Traj ledger: holdout gate + non-empty line count only.
	if e := findEntry(rep, TypeTrajInsight, "project", "trajectory-learnings"); e == nil || e.PromotionGate != GateHoldout || e.Items != 2 {
		t.Errorf("traj entry wrong: %+v", e)
	}
}

func TestBuildHashMatchesContent(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	content := "hash me"
	writeFile(t, filepath.Join(ws, ".ggcode/memory/notes.md"), content)

	rep, err := Build(ws, home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	e := findEntry(rep, TypeMemory, "project", "notes")
	if e == nil {
		t.Fatal("memory entry missing")
	}
	sum := sha256.Sum256([]byte(content))
	want := hex.EncodeToString(sum[:])[:12]
	if e.SHA256 != want {
		t.Errorf("sha256 = %s, want %s", e.SHA256, want)
	}
	if e.Bytes != int64(len(content)) {
		t.Errorf("bytes = %d, want %d", e.Bytes, len(content))
	}
}

func TestBuildEmptyStoresNoError(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	rep, err := Build(ws, home)
	if err != nil {
		t.Fatalf("Build on empty dirs: %v", err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("entries = %d, want 0", len(rep.Entries))
	}
}

func TestBuildMalformedDisabledStateStillUsable(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	writeFile(t, filepath.Join(ws, ".ggcode/skills/ok/SKILL.md"), "# ok\n")
	writeFile(t, filepath.Join(home, ".ggcode/disabled_skills.json"), "{not json")

	rep, err := Build(ws, home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if e := findEntry(rep, TypeSkill, "project", "ok"); e == nil || !e.Enabled {
		t.Errorf("malformed disabled state must not disable skills: %+v", e)
	}
}

func TestBuildInvalidPaths(t *testing.T) {
	if _, err := Build("", "/home"); err == nil {
		t.Error("empty workspace should error")
	}
	if _, err := Build("/ws", ""); err == nil {
		t.Error("empty home should error")
	}
}

func TestBuildDeterministicOrder(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	for _, n := range []string{"b-skill", "a-skill", "c-skill"} {
		writeFile(t, filepath.Join(ws, ".ggcode/skills", n, "SKILL.md"), "# "+n+"\n")
	}
	rep, err := Build(ws, home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var names []string
	for _, e := range rep.Entries {
		if e.Type == TypeSkill {
			names = append(names, e.Name)
		}
	}
	want := "a-skill,b-skill,c-skill"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}
