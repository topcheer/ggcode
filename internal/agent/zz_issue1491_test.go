package agent

import (
	"testing"
)

func TestIssue1491D_DeepAbsoluteSignatureDiscriminates(t *testing.T) {
	if dirSignature("/Volumes/x/repo/internal/agent") == dirSignature("/Volumes/x/repo/internal/tui") {
		t.Fatal("sibling directories under a deep absolute root must not share a signature")
	}
	if got := dirSignature("/Volumes/x/repo/internal/agent"); got != "internal/agent" {
		t.Fatalf("absolute path should keep its last two segments, got %q", got)
	}

	if got := dirSignature("src/components"); got != "src/components" {
		t.Fatalf("relative path keeps top-2, got %q", got)
	}
}
