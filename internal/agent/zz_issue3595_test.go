package agent

import "testing"

func TestIssue3595_SpaceIndentedRecipeIsNotTampering(t *testing.T) {
	mk := "test:\n    go test ./...\n\nbuild:\n    go build ./...\n"
	if hasMakefileTamperingContent(mk) {
		t.Fatal("4-space-indented recipes misreported as tampering (commands present, wrong indentation)")
	}
}

func TestIssue3595_TabRecipesStillParse(t *testing.T) {

	ok := "test:\n\tgo test ./...\n"
	if hasMakefileTamperingContent(ok) {
		t.Fatal("tab-indented real recipe misreported as tampering")
	}

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

	if !hasMakefileTamperingContent("build:\n\tgo build ./...\n\ntest:\n") {
		t.Fatal("genuinely emptied test target no longer flagged")
	}
}
