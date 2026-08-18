package wailskit

import (
	"strings"
	"testing"
)

// Issue #704: the JSON session export truncated BEFORE redaction (opposite
// order to the Markdown path). A secret straddling the 2000-byte cut lost
// its matchable tail — a ghp_ token cut to fewer than the github_token
// pattern's {36,} minimum matched nothing and leaked plaintext token
// characters into the exported JSON artifact. Redaction must run on the
// FULL value first; truncation afterwards can at worst split the masked
// representation, which is harmless.

// build704Content places a full ghp_ token so that it straddles byte 2000:
// with the OLD order (truncate first) the retained side held "ghp_" + only
// 16 token chars — below the {36,} minimum — evading redaction entirely.
// The "=" before the token supplies the \b word boundary the pattern needs
// (mirrors the real "GITHUB_TOKEN=ghp_..." trigger).
func build704Content() string {
	token := "ghp_" + strings.Repeat("A", 40) // 44 chars, matches github_token
	prefix := strings.Repeat("x", 1978) + "=" // token starts at byte 1979
	return prefix + token + strings.Repeat("y", 200)
}

// TestIssue704_JSONExportRedactsBeforeTruncation pins the order for the
// Content field: with a boundary-straddling token, no plaintext token body
// (≥5 contiguous chars) may appear in the exported JSON — the token must
// be masked (first-4 + asterisks) BEFORE the 2000-byte cut.
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
	// With redact-first, byte 2000 lands inside the asterisk mask:
	// "...=ghp_************…". The old order left "=ghp_AAAAAAAAAAAAAAAA".
	if !strings.Contains(out, "ghp_"+strings.Repeat("*", 8)) {
		t.Errorf("expected masked token (ghp_ + asterisks) before the cut; got unmasked or unmatched token")
	}
}

// TestIssue704_JSONExportToolArgsRedactsBeforeTruncation pins the same
// order for the ToolArgs field (the old code truncated args first too).
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
