package tui

import "testing"

// zz_issue4268_test.go - structural guard for the #889/#2835/#4268 omission
// family: every command that has a description (i.e. is user-facing) must be
// present in the SlashCommands list so Tab completion offers it. This test
// replaces the per-command whack-a-mole that let the family recur 3 times.

func TestSlashCommandDescriptionsSubsetOfList_Issue4268(t *testing.T) {
	inList := make(map[string]bool, len(SlashCommands))
	for _, c := range SlashCommands {
		inList[c] = true
	}
	for cmd := range SlashCommandDescriptions {
		if !inList[cmd] {
			t.Errorf("command %q has a description but is missing from SlashCommands (Tab completion gap, #889/#2835/#4268 family)", cmd)
		}
	}
}

func TestSlashCommandsUnique_Issue4268(t *testing.T) {
	seen := make(map[string]bool, len(SlashCommands))
	for _, c := range SlashCommands {
		if seen[c] {
			t.Errorf("duplicate slash command %q in SlashCommands", c)
		}
		seen[c] = true
	}
}

func TestInterventionsRefusalsCompletable_Issue4268(t *testing.T) {
	inList := make(map[string]bool, len(SlashCommands))
	for _, c := range SlashCommands {
		inList[c] = true
	}
	for _, cmd := range []string{"/interventions", "/refusals"} {
		if !inList[cmd] {
			t.Errorf("%s must appear in SlashCommands for Tab completion", cmd)
		}
		if _, ok := SlashCommandDescriptions[cmd]; !ok {
			t.Errorf("%s must keep its description entry", cmd)
		}
	}
}
