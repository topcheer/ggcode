package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
)

// #4597: the "# routing-health" sentinel had no expiry path. Once written
// (library >= 40 && notices >= 6), shrinking the library below the warn
// threshold never removed it: detectMissedSkills early-returned on the
// no-hits path and nothing else ever rewrote the key, so the stale
// "N user skills, consider consolidating" line was injected into every
// subsequent session forever. The fix strips the sentinel on the no-hits
// path when the notice conditions no longer hold.

func seedMissedSkillKey(t *testing.T, wd, content string) {
	t.Helper()
	autoMem := memory.NewProjectAutoMemory(wd)
	if err := autoMem.SaveMemoryWithSource(missedSkillMemoryKey, content, "missed-skill"); err != nil {
		t.Fatal(err)
	}
}

// Stale sentinel must be stripped once the library shrinks below the warn
// threshold, even when no new missed-skill hit occurs.
func TestRoutingHealthSentinelExpiredWhenLibraryShrinks(t *testing.T) {
	wd := probeMissedSkillEnv(t, "browser-automation") // 1 skill < skillLibrarySizeWarn
	seedMissedSkillKey(t, wd,
		"skill old-skill matched a recent task's prompt but was not invoked - consider the skill tool or /skills list\n# routing-health: 45 user skills, 7 missed-skill notices - routing degrades with library size; consider consolidating or disabling low-signal skills (see /skills list)")

	// Prompt names no skill: the no-hits path must run the expiry check.
	stats := agent.RunStats{
		UserPrompt: "refactor the parser module and run the tests",
		ToolCalls:  map[string]int{"read_file": 1},
	}
	detectMissedSkills(wd, stats)

	got, err := memory.NewProjectAutoMemory(wd).LoadKey(missedSkillMemoryKey)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if strings.Contains(got, "# routing-health") {
		t.Fatalf("stale sentinel must be stripped when conditions clear (#4597), got: %q", got)
	}
	if !strings.Contains(got, "old-skill") {
		t.Fatalf("recorded notices must survive sentinel cleanup, got: %q", got)
	}
}

// Sentinel must survive when the conditions still hold (library large,
// notices accumulated): expiry is conditional, not unconditional wipe.
func TestRoutingHealthSentinelKeptWhileWarranted(t *testing.T) {
	wd := t.TempDir()
	skillsDir := filepath.Join(wd, ".ggcode", "skills")
	var lines []string
	for i := 0; i < skillLibrarySizeWarn; i++ {
		name := fmt.Sprintf("skill-%03d-padding-name", i)
		dir := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < skillRoutingHealthMinNotices; i++ {
		lines = append(lines, fmt.Sprintf("skill keep-%d matched a recent task's prompt but was not invoked - consider the skill tool or /skills list", i))
	}
	lines = append(lines, "# routing-health: "+fmt.Sprintf("%d user skills, %d missed-skill notices - routing degrades with library size; consider consolidating or disabling low-signal skills (see /skills list)", skillLibrarySizeWarn, skillRoutingHealthMinNotices))
	seedMissedSkillKey(t, wd, strings.Join(lines, "\n"))

	stats := agent.RunStats{
		UserPrompt: "unrelated topic entirely",
		ToolCalls:  map[string]int{"read_file": 1},
	}
	detectMissedSkills(wd, stats)

	got, _ := memory.NewProjectAutoMemory(wd).LoadKey(missedSkillMemoryKey)
	if !strings.Contains(got, "# routing-health") {
		t.Fatalf("warranted sentinel must be kept while conditions hold, got: %q", got)
	}
}

// Key without a sentinel is left byte-identical on the no-hits path.
func TestRoutingHealthNoSentinelNoTouch(t *testing.T) {
	wd := probeMissedSkillEnv(t, "browser-automation")
	content := "skill untouched matched a recent task's prompt but was not invoked - consider the skill tool or /skills list"
	seedMissedSkillKey(t, wd, content)

	stats := agent.RunStats{
		UserPrompt: "do something unrelated",
		ToolCalls:  map[string]int{"read_file": 1},
	}
	detectMissedSkills(wd, stats)

	got, _ := memory.NewProjectAutoMemory(wd).LoadKey(missedSkillMemoryKey)
	if got != content {
		t.Fatalf("key without sentinel must stay untouched, got: %q", got)
	}
}
