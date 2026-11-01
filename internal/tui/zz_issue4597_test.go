package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
)

// #4597: the routing-health sentinel must be stripped when the library later
// shrinks below the threshold, even on runs with no new misses (the no-hits
// early return used to skip the only sentinel-reevaluation path).
func TestIssue4597_StaleRoutingHealthStrippedWhenLibraryShrinks(t *testing.T) {
	wd := probeMissedSkillEnv(t, "browser-automation")

	// Seed the key with a sentinel + one notice, as a past large-library run
	// would have written.
	autoMem := memory.NewProjectAutoMemory(wd)
	seed := "skill browser-automation matched a recent task's prompt but was not invoked - consider the skill tool or /skills list\n# routing-health: 45 user skills, 7 missed-skill notices - routing degrades with library size; consider consolidating or disabling low-signal skills (see /skills list)"
	if err := autoMem.SaveMemoryWithSource(missedSkillMemoryKey, seed, "missed-skill"); err != nil {
		t.Fatal(err)
	}

	// A run with NO hits against the (now tiny, 1-skill) library.
	stats := agent.RunStats{
		UserPrompt: "unrelated work",
		ToolCalls:  map[string]int{"read_file": 1},
	}
	detectMissedSkills(wd, stats)

	got, err := autoMem.LoadKey(missedSkillMemoryKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "# routing-health") {
		t.Fatalf("stale routing-health sentinel must be stripped after the library shrank, got: %q", got)
	}
	if !strings.Contains(got, "browser-automation") {
		t.Fatalf("real notices must survive the strip, got: %q", got)
	}
}

// Threshold still met: the sentinel survives no-hit runs untouched.
func TestIssue4597_SentinelKeptWhenLibraryStillLarge(t *testing.T) {
	wd := probeMissedSkillEnv(t, "browser-automation")
	autoMem := memory.NewProjectAutoMemory(wd)
	seed := "skill browser-automation matched - consider the skill tool\n# routing-health: 45 user skills, 7 notices"
	if err := autoMem.SaveMemoryWithSource(missedSkillMemoryKey, seed, "missed-skill"); err != nil {
		t.Fatal(err)
	}

	// Library of 1 (< threshold) BUT pass a large size directly through the
	// helper: with the threshold met, nothing changes.
	maybeStripStaleRoutingHealth(wd, skillLibrarySizeWarn+5)

	got, _ := autoMem.LoadKey(missedSkillMemoryKey)
	if !strings.Contains(got, "# routing-health") {
		t.Fatalf("sentinel must be kept while the library meets the threshold, got: %q", got)
	}
	_ = os.RemoveAll(filepath.Join(wd)) // best-effort cleanup of temp env
}
