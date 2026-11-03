package tui

import (
	"strings"
	"testing"
)

func TestIssue2842PTHelpTextLocalized(t *testing.T) {
	got := ptCatalog("help.text")
	if got == "" || got == "help.text" {
		t.Fatalf("#2842 ptCatalog(help.text) empty/raw: %q", got)
	}
	if got == enCatalog("help.text") {
		t.Fatalf("#2842 pt help.text is the English text (untranslated passthrough)")
	}
	if !strings.Contains(got, "Comandos dispon") {
		t.Errorf("#2842 pt help.text does not look Portuguese: %q", got[:40])
	}

	for _, section := range []string{"Sess", "Modelo", "Sistema", "Atalhos"} {
		if !strings.Contains(got, section) {
			t.Errorf("#2842 pt help.text missing section %q", section)
		}
	}
}

func TestIssue2843PTHasNoDeadKeys(t *testing.T) {

	for _, key := range []string{
		"label.cancel", "label.save", "label.delete", "label.search",
		"doctor.title", "doctor.ok", "doctor.fail",
		"welcome.title", "welcome.subtitle",
		"mode.auto", "mode.plan", "mode.supervised",
		"status.agent_idle", "status.agent_running",
		"input.hint", "input.cancel",
		"error.workspace_not_git", "slash.doctor", "reasoning.label",
	} {

		if pt := ptCatalog(key); pt != "" && pt != enCatalog(key) {
			t.Errorf("#2843 dead key %q still carries a pt-specific value: %q", key, pt)
		}
	}
}
