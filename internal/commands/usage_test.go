package commands

import (
	"testing"
	"time"
)

func TestRecordUsageAndUsageScore(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	if err := RecordUsage("deploy"); err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	score := UsageScore("deploy")
	if score <= 0 {
		t.Fatalf("UsageScore = %v, want > 0", score)
	}
}

func TestManagerListRanksFrequentlyUsedSkillsFirst(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	now := time.Now().UnixMilli()
	skillUsageMu.Lock()
	if err := saveUsageLocked(map[string]skillUsageEntry{
		"high": {UsageCount: 10, LastUsedAt: now},
		"low":  {UsageCount: 1, LastUsedAt: now},
	}); err != nil {
		skillUsageMu.Unlock()
		t.Fatalf("saveUsageLocked error = %v", err)
	}
	skillUsageMu.Unlock()

	mgr := &Manager{
		commands: map[string]*Command{
			"low":  {Name: "low", Source: SourceUser, LoadedFrom: LoadedFromSkills},
			"high": {Name: "high", Source: SourceUser, LoadedFrom: LoadedFromSkills},
		},
	}

	list := mgr.List()
	if len(list) < 2 {
		t.Fatalf("len(list) = %d", len(list))
	}
	if list[0].Name != "high" {
		t.Fatalf("first skill = %q, want %q", list[0].Name, "high")
	}
}

func TestManagerUserSlashCommandsOnlyIncludesUserInvocableSkills(t *testing.T) {
	mgr := &Manager{
		commands: map[string]*Command{
			"deploy": {Name: "deploy", UserInvocable: true},
			"debug":  {Name: "debug", UserInvocable: false},
		},
	}

	cmds := mgr.UserSlashCommands()
	if len(cmds) != 1 {
		t.Fatalf("len(cmds) = %d, want 1", len(cmds))
	}
	if _, ok := cmds["deploy"]; !ok {
		t.Fatal("expected deploy in slash commands")
	}
	if _, ok := cmds["debug"]; ok {
		t.Fatal("did not expect debug in slash commands")
	}
}
