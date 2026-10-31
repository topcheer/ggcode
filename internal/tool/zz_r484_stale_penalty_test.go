package tool

// #r484 probe (SkillForge pruning core / sa-23 round-23 research):
// lexical retrieval scores must decay with last-used age so stale skills
// sink in ranking, while never-used skills and strong lexical matches are
// protected (demote, never erase).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSkillUsage(t *testing.T, dir string, entries map[string]time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]skillUsageLite{}
	for name, ts := range entries {
		payload[name] = skillUsageLite{LastUsed: ts}
	}
	data := []byte("{")
	first := true
	for name, e := range payload {
		if !first {
			data = append(data, ',')
		}
		first = false
		data = append(data, `"`+name+`":{"last_used":"`+e.LastUsed.UTC().Format(time.RFC3339Nano)+`"}`...)
	}
	data = append(data, '}')
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "skill-usage.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestR484_NoUsageFileZeroImpact(t *testing.T) {
	dir := t.TempDir()
	idx := loadSkillUsageIndex(dir)
	if len(idx) != 0 {
		t.Fatalf("missing file must yield empty index, got %d", len(idx))
	}
	if stalePenalty(idx, "deploy-aws", time.Now()) != 0 {
		t.Fatal("no record -> no penalty (new skills must not be buried)")
	}
}

func TestR484_StaleThresholds(t *testing.T) {
	now := time.Now()
	idx := skillUsageIndex{
		"fresh":  {LastUsed: now.Add(-7 * 24 * time.Hour)},
		"stale":  {LastUsed: now.Add(-45 * 24 * time.Hour)},
		"zombie": {LastUsed: now.Add(-120 * 24 * time.Hour)},
		"zero":   {},
	}
	if stalePenalty(idx, "fresh", now) != 0 {
		t.Fatal("7-day-old skill is not stale")
	}
	if stalePenalty(idx, "stale", now) != 4 {
		t.Fatal("45-day-old skill must take the 30-day penalty (knight notice threshold)")
	}
	if stalePenalty(idx, "zombie", now) != 8 {
		t.Fatal("120-day-old skill must take the deep-stale penalty")
	}
	if stalePenalty(idx, "zero", now) != 0 {
		t.Fatal("zero LastUsed record -> no penalty")
	}
	if stalePenalty(idx, "unknown", now) != 0 {
		t.Fatal("unknown skill -> no penalty")
	}
}

func TestR484_RankingDemotesStaleOverFresh(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeSkillUsage(t, dir, map[string]time.Time{
		"deploy-aws": now.Add(-120 * 24 * time.Hour), // deep stale
		"deploy-gcp": now.Add(-1 * 24 * time.Hour),   // fresh
	})
	skills := makeFakeSkills()
	tool := SkillTool{
		Skills:     skills,
		NameLister: skills,
		WorkingDir: dir,
	}
	matches := tool.collectSkillMatches(skills.SkillNames(), "deploy")
	if len(matches) < 2 {
		t.Fatalf("both deploy skills must match, got %d", len(matches))
	}
	var awsScore, gcpScore int
	for _, m := range matches {
		switch m.name {
		case "deploy-aws":
			awsScore = m.score
		case "deploy-gcp":
			gcpScore = m.score
		}
	}
	// Both are substring name matches (10) + desc hits; the stale one must
	// rank strictly below the fresh one.
	if awsScore >= gcpScore {
		t.Fatalf("stale deploy-aws (%d) must rank below fresh deploy-gcp (%d)", awsScore, gcpScore)
	}
	if awsScore < 1 {
		t.Fatal("demote never erases: strong lexical match must stay >= 1")
	}
}

func TestR484_CorruptUsageFileDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "skill-usage.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if idx := loadSkillUsageIndex(dir); len(idx) != 0 {
		t.Fatal("corrupt file must degrade to empty index (pure-lexical behavior)")
	}
}
