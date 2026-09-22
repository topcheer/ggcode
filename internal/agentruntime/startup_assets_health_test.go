package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

func TestSkillsPromptHealthAwareOrderingAndMarker(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// zskill: 3 recorded failures; askill: never executed.
	for i := 0; i < 3; i++ {
		if err := commands.RecordOutcome("zskill", false); err != nil {
			t.Fatalf("RecordOutcome error = %v", err)
		}
	}

	skills := []*commands.Command{
		{Name: "zskill", Description: "rarely works", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Enabled: true},
		{Name: "askill", Description: "solid workflow", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Enabled: true},
	}

	prompt, _ := BuildSkillsSystemPromptWithPromptRefs(skills)
	aIdx := strings.Index(prompt, "- askill:")
	zIdx := strings.Index(prompt, "- zskill:")
	if aIdx < 0 || zIdx < 0 {
		t.Fatalf("prompt missing skill lines:\n%s", prompt)
	}
	if zIdx < aIdx {
		t.Fatalf("failing skill zskill listed before healthy askill:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[health: failed 3/3 recent runs") {
		t.Fatalf("failing skill lacks health marker:\n%s", prompt)
	}
	if strings.Contains(prompt, "[health:") && strings.Count(prompt, "[health:") != 1 {
		t.Fatalf("health marker should only appear on failing skills:\n%s", prompt)
	}
}

func TestSkillsPromptNoMarkerWithoutOutcomes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	skills := []*commands.Command{
		{Name: "fresh", Description: "never run", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Enabled: true},
	}
	prompt, _ := BuildSkillsSystemPromptWithPromptRefs(skills)
	if strings.Contains(prompt, "[health:") {
		t.Fatalf("health marker must not appear without recorded outcomes:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- fresh: never run") {
		t.Fatalf("prompt missing skill line:\n%s", prompt)
	}
}

func TestPrioritizedSkillsFailingDemotedWithinTier(t *testing.T) {
	stats := map[string]commands.OutcomeStat{
		"a-fail": {Runs: 4, Successes: 0, Failures: 4},
		"b-fail": {Runs: 2, Successes: 0, Failures: 2}, // below min samples: not failing
	}
	skills := []*commands.Command{
		{Name: "b-fail", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Enabled: true},
		{Name: "a-fail", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Enabled: true},
		{Name: "c-ok", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Enabled: true},
	}
	out := prioritizedSkillsForPrompt(skills, stats)
	got := []string{out[0].Name, out[1].Name, out[2].Name}
	want := []string{"b-fail", "c-ok", "a-fail"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
