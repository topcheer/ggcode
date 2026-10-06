package memory

// sa-72 / Hindsight (arXiv:2512.12818) opinion-coherence probe: the
// preference store must supersede, not coexist, when a newer reversal
// statement revokes a prior choice on the same preference slot. The old
// behavior injected "always use npm" AND "以后改用 pnpm" into every future
// prompt - the evidence/inference blur the paper flags.

import (
	"strings"
	"testing"
)

func TestSupersession_ZH_ReversalReplacesOldChoice(t *testing.T) {
	existing := "- 以后都用 npm 安装依赖\n"
	merged, added := MergePreferenceMemory(existing, []string{"以后改用 pnpm 装依赖"})
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	// Assert on the full revoked sentence: a bare "npm" substring check
	// false-positives because "pnpm" CONTAINS "npm".
	if strings.Contains(merged, "以后都用 npm") {
		t.Fatalf("revoked npm preference must be superseded, got %q", merged)
	}
	if !strings.Contains(merged, "pnpm") {
		t.Fatalf("new preference must be present, got %q", merged)
	}
}

func TestSupersession_EN_ReversalReplacesOldChoice(t *testing.T) {
	existing := "- always use npm for installs\n"
	merged, added := MergePreferenceMemory(existing, []string{"no, use pnpm instead"})
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if strings.Contains(merged, "always use npm") {
		t.Fatalf("revoked npm choice must be superseded, got %q", merged)
	}
	if !strings.Contains(merged, "pnpm") {
		t.Fatalf("new choice must be present, got %q", merged)
	}
}

func TestSupersession_SwitchBackReplaces(t *testing.T) {
	// The symmetric case: revoking a revocation ("switch back").
	existing := "- going forward, switch to pnpm for installs\n"
	merged, _ := MergePreferenceMemory(existing, []string{"switch back to npm please"})
	if strings.Contains(merged, "pnpm") {
		t.Fatalf("pnpm choice must be superseded by switch-back, got %q", merged)
	}
	if !strings.Contains(merged, "switch back to npm") {
		t.Fatalf("switch-back statement must be present, got %q", merged)
	}
}

func TestSupersession_ReversalWithoutKnownSlotDeletesNothing(t *testing.T) {
	// Precision guard: a reversal whose tokens carry NO known slot must not
	// delete unrelated entries (conservative - miss beats wrong delete).
	existing := "- always use npm for installs\n- remember to run the linter twice\n"
	merged, added := MergePreferenceMemory(existing, []string{"don't use that weird editor anymore"})
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if !strings.Contains(merged, "always use npm") || !strings.Contains(merged, "run the linter twice") {
		t.Fatalf("unknown-slot reversal must delete nothing, got %q", merged)
	}
}

func TestSupersession_NonReversalSameSlotCoexists(t *testing.T) {
	// A plain (non-reversal) statement never deletes, even in a known slot -
	// deleting on positive statements risks removing real user intent.
	existing := "- always use npm for installs\n"
	merged, _ := MergePreferenceMemory(existing, []string{"always run npm with --silent"})
	if !strings.Contains(merged, "always use npm") || !strings.Contains(merged, "--silent") {
		t.Fatalf("non-reversal same-slot statements must coexist, got %q", merged)
	}
}

func TestSupersession_OnlySameSlotEntriesDropped(t *testing.T) {
	// A reversal on the shell slot must not touch the pkg-manager entry.
	existing := "- always use npm for installs\n- from now on run scripts with bash\n"
	merged, _ := MergePreferenceMemory(existing, []string{"no, run scripts with zsh instead"})
	if !strings.Contains(merged, "always use npm") {
		t.Fatalf("different-slot entry must survive, got %q", merged)
	}
	if strings.Contains(merged, "bash") {
		t.Fatalf("same-slot (shell) entry must be superseded, got %q", merged)
	}
}

func TestSupersession_DuplicateDedupStillWorks(t *testing.T) {
	// Regression guard: exact-duplicate behavior is unchanged.
	existing := "- always use npm\n"
	merged, added := MergePreferenceMemory(existing, []string{"always use npm"})
	if added != 0 {
		t.Fatalf("duplicate must add 0, got %d", added)
	}
	if strings.Count(merged, "always use npm") != 1 {
		t.Fatalf("exactly one copy expected, got %q", merged)
	}
}

func TestSupersession_GoFmtTokenBoundary(t *testing.T) {
	// #3311 lesson applied to slots: "gofmt" contains "go"-like substrings
	// only via word boundary; a formatter reversal must key on the real
	// token and a sentence mentioning neither formatter must yield no slot.
	if got := preferenceSlotsOf("always format with gofmt before commits"); len(got) != 1 || got[0] != "go-formatter" {
		t.Fatalf("gofmt sentence slots = %v, want [go-formatter]", got)
	}
	if got := preferenceSlotsOf("let's go home now"); len(got) != 0 {
		t.Fatalf("ordinary 'go' must carry no slot, got %v", got)
	}
}
