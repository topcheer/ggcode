package wailskit

import (
	"strings"
	"testing"
)

func build704Content() string {
	token := "ghp_" + strings.Repeat("A", 40)
	prefix := strings.Repeat("x", 1978) + "="
	return prefix + token + strings.Repeat("y", 200)
}

func TestIssue704_JSONExportRedactsBeforeTruncation(t *testing.T) {
	msgs := []SessionMessage{
		{Role: "tool", Content: build704Content()},
	}
	out, err := formatMessagesAsJSON(msgs, "t")
	if err != nil {
		t.Fatalf("formatMessagesAsJSON: %v", err)
	}
	if strings.Contains(out, strings.Repeat("A", 5)) {
		t.Errorf("plaintext token chars leaked into JSON export Content; want masked before truncation")
	}

	if !strings.Contains(out, "ghp_"+strings.Repeat("*", 8)) {
		t.Errorf("expected masked token (ghp_ + asterisks) before the cut; got unmasked or unmatched token")
	}
}

func TestIssue704_JSONExportToolArgsRedactsBeforeTruncation(t *testing.T) {
	msgs := []SessionMessage{
		{Role: "tool", ToolArgs: build704Content()},
	}
	out, err := formatMessagesAsJSON(msgs, "t")
	if err != nil {
		t.Fatalf("formatMessagesAsJSON: %v", err)
	}
	if strings.Contains(out, strings.Repeat("A", 5)) {
		t.Errorf("plaintext token chars leaked into JSON export ToolArgs; want masked before truncation")
	}
	if !strings.Contains(out, "ghp_"+strings.Repeat("*", 8)) {
		t.Errorf("expected masked token (ghp_ + asterisks) before the cut; got unmasked or unmatched token")
	}
}
