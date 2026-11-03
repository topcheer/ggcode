//go:build goolm

package memory

import "testing"

func TestIssue3920_UnderscoreSiblingDoesNotConsumeShorterKey(t *testing.T) {
	text := "See release_process_v2 for the current flow."
	if containsStandaloneKey(text, "release_process") {
		t.Fatal("snake_case sibling must NOT count as consuming the shorter key (#3920)")
	}

	if !containsStandaloneKey(text, "release_process_v2") {
		t.Fatal("exact key must still count")
	}

	if !containsStandaloneKey("see release_process for details", "release_process") {
		t.Fatal("space-bounded key must count")
	}
	if !containsStandaloneKey("see (release_process) for details", "release_process") {
		t.Fatal("paren-bounded key must count")
	}

	if containsStandaloneKey("See release-process-v2 now.", "release-process") {
		t.Fatal("dash sibling must stay excluded (#3827)")
	}

	if containsStandaloneKey("See ReleaseProcessV2 for details.", "ReleaseProcess") {
		t.Fatal("CamelCase sibling must not count (sanitizeKey whitelist keeps A-Z)")
	}
	if !containsStandaloneKey("see ReleaseProcess for details", "ReleaseProcess") {
		t.Fatal("exact CamelCase key must count")
	}
}
