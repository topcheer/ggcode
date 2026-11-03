package tui

import "testing"

func TestIssue2835SlashCompletionTablesCoverHandlers(t *testing.T) {
	for _, cmd := range []string{"/pin", "/style", "/goal"} {
		found := false
		for _, c := range SlashCommands {
			if c == cmd {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("#2835 %s missing from SlashCommands list (Tab completion cannot offer it)", cmd)
		}
		if _, ok := SlashCommandDescriptions[cmd]; !ok {
			t.Errorf("#2835 %s missing from SlashCommandDescriptions", cmd)
		}
		if _, ok := SlashCommandPlaceholders[cmd]; !ok {
			t.Errorf("#2835 %s missing from SlashCommandPlaceholders", cmd)
		}
	}

	for _, c := range SlashCommands {
		if _, ok := SlashCommandDescriptions[c]; !ok {
			t.Errorf("SlashCommands entry %q has no description (partial table update)", c)
		}
	}
}
