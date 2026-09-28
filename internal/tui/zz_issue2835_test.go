package tui

import "testing"

// zz_issue2835_test.go - probe for #2835: /pin, /style, /goal have live
// dispatch handlers (commands.go) but were missing from all three completion
// tables in completion.go, making them undiscoverable via Tab completion
// (#889 omission family recurrence).
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

	// Guard against the inverse drift: every listed command with a handler
	// contract keeps a description (list-without-description is the #889
	// signature of partial table updates).
	for _, c := range SlashCommands {
		if _, ok := SlashCommandDescriptions[c]; !ok {
			t.Errorf("SlashCommands entry %q has no description (partial table update)", c)
		}
	}
}
