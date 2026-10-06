package tui

import (
	"strings"
	"testing"
)

// zz_issue2842_2843_test.go - probes for the i18n_pt cleanup pair.
// #2842: pt was the only locale of 10 missing help.text - the whole /help
// page fell back to English. #2843: pt carried 69 keys the en universe
// (base + module catalogs) had deleted, with zero consumers repo-wide.
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
	// Structural parity: the localized page covers the command sections.
	for _, section := range []string{"Sess", "Modelo", "Sistema", "Atalhos"} {
		if !strings.Contains(got, section) {
			t.Errorf("#2842 pt help.text missing section %q", section)
		}
	}
}

func TestIssue2843PTHasNoDeadKeys(t *testing.T) {
	// Probe the known-removed families: each of these keys was deleted from
	// pt (69 keys, zero consumers). If any still resolves to a pt-specific
	// value (differing from the en fallback path), the prune missed a case.
	for _, key := range []string{
		"label.cancel", "label.save", "label.delete", "label.search",
		"doctor.title", "doctor.ok", "doctor.fail",
		"welcome.title", "welcome.subtitle",
		"mode.auto", "mode.plan", "mode.supervised",
		"status.agent_idle", "status.agent_running",
		"input.hint", "input.cancel",
		"error.workspace_not_git", "slash.doctor", "reasoning.label",
	} {
		// The prune removes the pt case entirely; resolution then falls to
		// enCatalog. We cannot call the internal switch directly for a
		// "case exists" check, but a pt-translated value distinct from en
		// proves the case survived the prune.
		if pt := ptCatalog(key); pt != "" && pt != enCatalog(key) {
			t.Errorf("#2843 dead key %q still carries a pt-specific value: %q", key, pt)
		}
	}
}
