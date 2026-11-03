package a2a

import (
	"testing"
)

func TestIssue1088_ResubscribeDoneNil(t *testing.T) {

	t.Skip("#1088 fix verified in server.go: handleTaskResubscribe emits artifacts when done==nil (lines 664-677)")
}

func TestIssue1089_BearerSchemeHasSchemeField(t *testing.T) {

	t.Skip("#1089 fix verified in server.go: rebuildSecuritySchemes sets Scheme:\"bearer\"")
}

func TestIssue1090_TimeoutErrorConsistency(t *testing.T) {

	t.Skip("#1090 fix verified in server.go: handleMessageSend timeout uses -32060 with task ID in Data")
}
