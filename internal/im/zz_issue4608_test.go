package im

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4608_NoRetryReplyOnUnboundChannel(t *testing.T) {
	b, err := os.ReadFile("slack_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	retryAt := strings.Index(src, "message could not be delivered (session not ready), please retry")
	if retryAt < 0 {
		t.Fatal("generic failure reply line not found - slack_adapter.go drifted")
	}
	branchAt := strings.Index(src, "if err == ErrNoChannelBound {")
	if branchAt < 0 || branchAt > retryAt {
		t.Fatal("ErrNoChannelBound branch must guard BEFORE the generic failure reply (#4608)")
	}

	window := src[branchAt:min(branchAt+400, len(src))]
	if !strings.Contains(window, "return") {
		t.Fatal("ErrNoChannelBound branch must return without replying (#4608)")
	}
}
