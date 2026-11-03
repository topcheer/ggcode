package tui

import (
	"strings"
	"testing"
)

func TestSuggestionTaskKeyStripsDatePrefix(t *testing.T) {
	day1 := `- [2026-10-14] release the app (tools: run_command×5, edit_file×2) — recurring workflow; consider create_skill to persist it as an invocable skill`
	day3 := `- [2026-10-16] release the app (tools: git_commit×3) — recurring workflow; consider create_skill to persist it as an invocable skill`

	k1, k3 := suggestionTaskKey(day1), suggestionTaskKey(day3)
	if k1 != k3 {
		t.Fatalf("same workflow on different days must dedupe: %q vs %q", k1, k3)
	}
	if want := "release the app"; k1 != want {
		t.Fatalf("key must be the bare task text, got %q (want %q)", k1, want)
	}
	if strings.HasPrefix(k1, "[") || strings.Contains(k1, "2026") {
		t.Fatalf("key must not carry the date prefix: %q", k1)
	}

	plain := `release the app (tools: run_command×5)`
	if got := suggestionTaskKey(plain); got != "release the app" {
		t.Fatalf("plain line key: got %q", got)
	}

	if got := suggestionTaskKey("  release the app  "); got != "release the app" {
		t.Fatalf("bare line key: got %q", got)
	}

	if got := suggestionTaskKey("- [unterminated task text"); got != "- [unterminated task text" {
		t.Fatalf("malformed prefix should keep the raw line, got %q", got)
	}
}

func TestMergeSkillSuggestionsCrossDayReplace(t *testing.T) {
	existing := "- [2026-10-14] release the app (tools: run_command×5) — recurring workflow; consider create_skill to persist it as an invocable skill"
	fresh := "- [2026-10-16] release the app (tools: git_commit×3) — recurring workflow; consider create_skill to persist it as an invocable skill"

	merged := mergeSkillSuggestions(existing, fresh)
	if got := strings.Count(merged, "release the app"); got != 1 {
		t.Fatalf("cross-day re-suggestion must replace in place, found %d lines:\n%s", got, merged)
	}
	if !strings.Contains(merged, "[2026-10-16]") {
		t.Fatalf("merged list must keep the freshest line:\n%s", merged)
	}
}
