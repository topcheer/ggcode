package tui

// r398 probes: missed-skill detection (SAGE lineage, arXiv:2512.17102).
// A skill whose topic the user named in the prompt while the run never
// touched the skill tool is the routing failure SAGE identifies.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
)

func probeMissedSkillEnv(t *testing.T, skillName string) string {
	t.Helper()
	wd := t.TempDir()
	dir := filepath.Join(wd, ".ggcode", "skills", skillName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+skillName), 0644); err != nil {
		t.Fatal(err)
	}
	return wd
}

// r477 probes: routing-health sentinel (skill-selection scaling-limits
// watch). Pure-function coverage of the size x accumulated-notices gate.
func TestSkillRoutingHealthNoticeGate(t *testing.T) {
	cases := []struct {
		name    string
		lib     int
		notices int
		wantOn  bool
	}{
		{"small library many notices stays silent", 12, 10, false},
		{"large library few notices stays silent", 40, 3, false},
		{"large library at threshold stays silent", 39, 10, false},
		{"notices at threshold stays silent", 40, 5, false},
		{"large library degrading notices fires", 40, 6, true},
		{"very large library many notices fires", 96, 10, true},
		{"boundary exactly on both thresholds fires", skillLibrarySizeWarn, skillRoutingHealthMinNotices, true},
		{"zero notices never fires", 96, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := skillRoutingHealthNotice(c.lib, c.notices)
			if c.wantOn && got == "" {
				t.Fatalf("expected sentinel line for lib=%d notices=%d, got empty", c.lib, c.notices)
			}
			if !c.wantOn && got != "" {
				t.Fatalf("expected silence for lib=%d notices=%d, got: %q", c.lib, c.notices, got)
			}
			if c.wantOn {
				if !strings.HasPrefix(got, "# routing-health:") {
					t.Fatalf("sentinel must start with '# routing-health:' so mergeMissedSkills skips it, got: %q", got)
				}
				if !strings.Contains(got, "/skills list") {
					t.Fatalf("sentinel should point the user at /skills list, got: %q", got)
				}
			}
		})
	}
}

// The sentinel must survive merge round-trips only via live recomputation:
// a "#"-prefixed line is dropped by mergeMissedSkills, so a stale sentinel
// never lingers when conditions clear.
func TestSkillRoutingHealthSentinelNotAccumulatedByMerge(t *testing.T) {
	existing := "# routing-health: 40 user skills, 6 missed-skill notices - old\nskill foo matched a recent task's prompt but was not invoked - consider the skill tool or /skills list"
	merged := mergeMissedSkills(existing, []string{"bar"})
	if strings.Contains(merged, "# routing-health") {
		t.Fatalf("merge must drop stale sentinel lines (recomputed on write), got: %q", merged)
	}
	if !strings.Contains(merged, "bar") {
		t.Fatalf("merge must still record the new hit, got: %q", merged)
	}
}

func TestMissedSkillPromptNamesSkillNotInvoked(t *testing.T) {
	wd := probeMissedSkillEnv(t, "browser-automation")
	stats := agent.RunStats{
		UserPrompt: "please run the browser-automation flow for me",
		ToolCalls:  map[string]int{"read_file": 3},
	}
	detectMissedSkills(wd, stats)
	autoMem := memory.NewProjectAutoMemory(wd)
	got, err := autoMem.LoadKey(missedSkillMemoryKey)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(got, "browser-automation") {
		t.Fatalf("expected missed-skill notice for browser-automation, got: %q", got)
	}
}

func TestMissedSkillSkippedWhenSkillToolUsed(t *testing.T) {
	wd := probeMissedSkillEnv(t, "browser-automation")
	stats := agent.RunStats{
		UserPrompt: "please run the browser-automation flow for me",
		ToolCalls:  map[string]int{"skill": 1},
	}
	detectMissedSkills(wd, stats)
	autoMem := memory.NewProjectAutoMemory(wd)
	got, _ := autoMem.LoadKey(missedSkillMemoryKey)
	if strings.Contains(got, "browser-automation") {
		t.Fatalf("no notice expected when skill tool was used, got: %q", got)
	}
}

func TestMissedSkillNoTopicHitNoNotice(t *testing.T) {
	wd := probeMissedSkillEnv(t, "browser-automation")
	stats := agent.RunStats{
		UserPrompt: "fix the nil map write in server.go",
		ToolCalls:  map[string]int{"read_file": 2},
	}
	detectMissedSkills(wd, stats)
	autoMem := memory.NewProjectAutoMemory(wd)
	got, _ := autoMem.LoadKey(missedSkillMemoryKey)
	if got != "" {
		t.Fatalf("no notice expected for unrelated prompt, got: %q", got)
	}
}

func TestMissedSkillShortGenericNamesIgnored(t *testing.T) {
	wd := probeMissedSkillEnv(t, "debug")
	stats := agent.RunStats{
		UserPrompt: "debug the crash in module x",
		ToolCalls:  map[string]int{"read_file": 2},
	}
	detectMissedSkills(wd, stats)
	autoMem := memory.NewProjectAutoMemory(wd)
	got, _ := autoMem.LoadKey(missedSkillMemoryKey)
	if got != "" {
		t.Fatalf("short generic skill name must not match, got: %q", got)
	}
}

func TestMissedSkillMergeDedupesPerSkill(t *testing.T) {
	merged := mergeMissedSkills("", []string{"browser-automation"})
	if !strings.Contains(merged, "browser-automation") {
		t.Fatalf("first merge failed: %q", merged)
	}
	merged2 := mergeMissedSkills(merged, []string{"browser-automation"})
	if strings.Count(merged2, "browser-automation") != strings.Count(merged, "browser-automation") {
		t.Fatalf("re-merge must dedupe: %q vs %q", merged, merged2)
	}
	merged3 := mergeMissedSkills(merged, []string{"appstore-pipeline"})
	if !strings.Contains(merged3, "appstore-pipeline") || !strings.Contains(merged3, "browser-automation") {
		t.Fatalf("different skill must append: %q", merged3)
	}
}

func TestNormMissedSkill(t *testing.T) {
	cases := map[string]string{
		"Browser-Automation": "browserautomation",
		"app store pipeline": "appstorepipeline",
		"ab":                 "ab",
	}
	for in, want := range cases {
		if got := normMissedSkill(in); got != want {
			t.Errorf("normMissedSkill(%q) = %q, want %q", in, got, want)
		}
	}
}
