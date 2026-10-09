package agent

// #3595 probe: recipes written with 4-space indentation are commands,
// not sabotage. hasMakefileTamperingContent only recognized tab-prefixed
// recipe lines, so space-indented commands fell to the rule-head default
// (no colon -> cur reset -> line ignored), leaving test targets with zero
// commands and misreporting innocent files as tampered.

import "testing"

func TestIssue3595_SpaceIndentedRecipeIsNotTampering(t *testing.T) {
	mk := "test:\n    go test ./...\n\nbuild:\n    go build ./...\n"
	if hasMakefileTamperingContent(mk) {
		t.Fatal("4-space-indented recipes misreported as tampering (commands present, wrong indentation)")
	}
}

func TestIssue3595_TabRecipesStillParse(t *testing.T) {
	// Strict tab shape must keep working: real command -> not tampering.
	ok := "test:\n\tgo test ./...\n"
	if hasMakefileTamperingContent(ok) {
		t.Fatal("tab-indented real recipe misreported as tampering")
	}
	// And a genuine no-op shell (tab or spaces) is still tampering.
	bad := "test:\n\t@echo 'tests passed'\n"
	if !hasMakefileTamperingContent(bad) {
		t.Fatal("tab-indented pure-echo no-op no longer flagged")
	}
	badSpaces := "test:\n    @echo 'tests passed'\n"
	if !hasMakefileTamperingContent(badSpaces) {
		t.Fatal("space-indented pure-echo no-op no longer flagged")
	}
}

func TestIssue3595_TrulyEmptyTargetStillFlags(t *testing.T) {
	// Target declared, no command lines at all (tab or spaces): the real
	// sabotage shape must keep firing.
	if !hasMakefileTamperingContent("build:\n\tgo build ./...\n\ntest:\n") {
		t.Fatal("genuinely emptied test target no longer flagged")
	}
}
