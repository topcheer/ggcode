package tui

// Behavior contract tests for the 2026-09-20 slash-completion review:
//  1. Subcommand-bearing commands (SlashCommandSubcommands) confirmed via
//     Enter/Tab open a SUBCOMMAND panel instead of executing the bare main
//     command.
//  2. Panel commands (/provider ...) keep executing directly (not listed in
//     the table, Enter falls through to normal submission).
//  3. "/cmd arg-prefix" typing filters the subcommand list live.

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMatchSubcommandContext(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		cursor int
		cmd    string
		prefix string
		ok     bool
	}{
		{"trailing space shows all", "/memory ", 8, "/memory", "", true},
		{"arg prefix filters", "/memory li", 10, "/memory", "li", true},
		{"no space = command word stage", "/memory", 7, "", "", false},
		{"past first arg = no completion", "/notify mode be", 15, "", "", false},
		{"unknown command", "/provider ", 10, "", "", false},
		{"cursor before space", "/memory li", 7, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, prefix, ok := matchSubcommandContext(tc.value, tc.cursor)
			if ok != tc.ok || cmd != tc.cmd || prefix != tc.prefix {
				t.Fatalf("matchSubcommandContext(%q,%d) = (%q,%q,%v), want (%q,%q,%v)",
					tc.value, tc.cursor, cmd, prefix, ok, tc.cmd, tc.prefix, tc.ok)
			}
		})
	}
}

// Guard: every command in SlashCommandSubcommands must exist in the real
// command list - a rename would otherwise leave a dangling entry that
// silently never triggers.
func TestSubcommandTableEntriesExist(t *testing.T) {
	known := make(map[string]bool, len(SlashCommands))
	for _, c := range SlashCommands {
		known[c] = true
	}
	for cmd, subs := range SlashCommandSubcommands {
		if !known[cmd] {
			t.Errorf("SlashCommandSubcommands entry %q not in SlashCommands", cmd)
		}
		if len(subs) == 0 {
			t.Errorf("SlashCommandSubcommands[%q] is empty", cmd)
		}
	}
}

func TestFilterSubcommands(t *testing.T) {
	if got := filterSubcommands("/memory", ""); !reflect.DeepEqual(got, []string{"list", "clear"}) {
		t.Fatalf("unfiltered = %v", got)
	}
	if got := filterSubcommands("/memory", "cl"); !reflect.DeepEqual(got, []string{"clear"}) {
		t.Fatalf("filtered 'cl' = %v", got)
	}
	if got := filterSubcommands("/memory", "zz"); got != nil {
		t.Fatalf("no match should be nil, got %v", got)
	}
}

// Enter on bare "/memory" must NOT execute the command: it fills "/memory "
// and opens the subcommand panel.
func TestEnterBareSubcommandCommandOpensPanel(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/memory")
	m.input.CursorEnd()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("update returned %T", updated)
	}
	if got := m2.input.Value(); got != "/memory " {
		t.Fatalf("input = %q, want %q", got, "/memory ")
	}
	if !m2.autoCompleteActive || m2.autoCompleteKind != "subslash" {
		t.Fatalf("panel not active: active=%v kind=%q", m2.autoCompleteActive, m2.autoCompleteKind)
	}
	if !reflect.DeepEqual(m2.autoCompleteItems, []string{"list", "clear"}) {
		t.Fatalf("panel items = %v", m2.autoCompleteItems)
	}
}

// Enter on a panel command must keep executing directly (no interception).
func TestEnterPanelCommandStillExecutes(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/provider")
	m.input.CursorEnd()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("update returned %T", updated)
	}
	if m2.input.Value() != "" {
		t.Fatalf("input = %q, want cleared (command executed)", m2.input.Value())
	}
	if m2.autoCompleteActive {
		t.Fatalf("panel should not be active for panel command")
	}
}

// Confirming a subcommand rewrites the input to "/cmd sub ".
func TestApplyAutoCompleteSubslash(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/memory li")
	m.input.CursorEnd()
	m.autoCompleteActive = true
	m.autoCompleteKind = "subslash"
	m.autoCompleteItems = []string{"list"}
	m.autoCompleteIndex = 0
	m.applyAutoComplete()
	if got := m.input.Value(); got != "/memory list " {
		t.Fatalf("input = %q, want /memory list ", got)
	}
	if m.autoCompleteActive {
		t.Fatalf("panel should close after confirming subcommand")
	}
}

// Live typing "/memory li" recomputes the panel to the filtered item.
func TestUpdateAutoCompleteSubcommandFiltering(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/memory li")
	m.input.CursorEnd()
	m.updateAutoComplete()
	if !m.autoCompleteActive || m.autoCompleteKind != "subslash" {
		t.Fatalf("subslash panel not active: active=%v kind=%q", m.autoCompleteActive, m.autoCompleteKind)
	}
	if !reflect.DeepEqual(m.autoCompleteItems, []string{"list"}) {
		t.Fatalf("items = %v, want [list]", m.autoCompleteItems)
	}
}
