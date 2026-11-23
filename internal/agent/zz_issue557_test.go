package agent

import (
	"testing"
)

func TestIssue557_InformationalPromptWithVerbInIdentifier(t *testing.T) {
	if !isInformationalRequest("What does the updateHandler function do?") {
		t.Error("identifier fragment 'updateHandler' must not count as action verb 'update'")
	}
	if !isInformationalRequest("How does the deleteEntry helper work?") {
		t.Error("identifier fragment 'deleteEntry' must not count as action verb 'delete'")
	}
}

func TestIssue557_ExecutablePromptStillGated(t *testing.T) {

	if isInformationalRequest("Please update the config file and fix the tests.") {
		t.Error("real action-verb prompt misclassified as informational")
	}
	if got := detectActions("please update and fix"); len(got) == 0 {
		t.Error("detectActions lost all verbs after word-boundary switch")
	}
	if got := detectActions("what does updateHandler do"); len(got) != 0 {
		t.Errorf("detectActions counted identifier fragments: %v", got)
	}
}
