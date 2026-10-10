//go:build goolm

package memory

import "testing"

// #3920: sanitizeKey's whitelist keeps `_`, but boundaryRune's
// key-extending charset omitted it - a snake_case SIBLING citation
// (release_process_v2) counted as consuming the shorter key
// (release_process), inflating its Consumed tally. #3827 fixed the
// dash-form sibling; this is the same hole for the underscore form.
func TestIssue3920_UnderscoreSiblingDoesNotConsumeShorterKey(t *testing.T) {
	text := "See release_process_v2 for the current flow."
	if containsStandaloneKey(text, "release_process") {
		t.Fatal("snake_case sibling must NOT count as consuming the shorter key (#3920)")
	}
	// The exact key still does.
	if !containsStandaloneKey(text, "release_process_v2") {
		t.Fatal("exact key must still count")
	}
	// Genuine boundary forms keep working both sides.
	if !containsStandaloneKey("see release_process for details", "release_process") {
		t.Fatal("space-bounded key must count")
	}
	if !containsStandaloneKey("see (release_process) for details", "release_process") {
		t.Fatal("paren-bounded key must count")
	}
	// Dash-form sibling stays excluded (#3827 regression pin).
	if containsStandaloneKey("See release-process-v2 now.", "release-process") {
		t.Fatal("dash sibling must stay excluded (#3827)")
	}
	// CamelCase sibling (review correction): sanitizeKey's whitelist keeps
	// A-Z, so the extending charset must cover it too.
	if containsStandaloneKey("See ReleaseProcessV2 for details.", "ReleaseProcess") {
		t.Fatal("CamelCase sibling must not count (sanitizeKey whitelist keeps A-Z)")
	}
	if !containsStandaloneKey("see ReleaseProcess for details", "ReleaseProcess") {
		t.Fatal("exact CamelCase key must count")
	}
}
