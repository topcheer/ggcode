package agent

// #3626 probe: an empty "all:" placeholder (no recipe, no deps) is a legal
// make idiom - make just prints "Nothing to be done". The old empty-branch
// flagged any test OR build target with zero commands after delegation
// resolution, so innocent placeholders were deterministically reported as
// tampering. Empty TEST targets keep firing.

import "testing"

func TestIssue3626_EmptyBuildPlaceholderNotTampering(t *testing.T) {
	if hasMakefileTamperingContent("all:\n\nother:\n\techo hi\n") {
		t.Fatal("empty all: placeholder misreported as tampering")
	}
	if hasMakefileTamperingContent("build:\n\n.PHONY: build\nother:\n\techo hi\n") {
		t.Fatal("empty build: placeholder misreported as tampering")
	}
}

func TestIssue3626_EmptyTestTargetStillFlags(t *testing.T) {
	if !hasMakefileTamperingContent("build:\n\tgo build ./...\n\ntest:\n") {
		t.Fatal("genuinely emptied test target no longer flagged")
	}
	// Deleted test with a surviving .PHONY trace next to an empty build
	// placeholder still fires (the #3610 form-3 signal).
	if !hasMakefileTamperingContent(".PHONY: build test\nbuild:\n") {
		t.Fatal("deleted-test .PHONY trace no longer flagged")
	}
}
