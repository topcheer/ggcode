package agent

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

	if !hasMakefileTamperingContent(".PHONY: build test\nbuild:\n") {
		t.Fatal("deleted-test .PHONY trace no longer flagged")
	}
}
