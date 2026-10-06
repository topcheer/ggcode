package provider

// Regression probes for #3077 (user_error.go, two points):
//   V1: the prefix-strip fallback branch returned the stripped error text
//       without sanitizeRawError - a #1720 bypass (only the blind-spot
//       branch was redacted; no outer caller re-sanitizes).
//   V2: the connection-refused check matched case-sensitively BEFORE the
//       ToLower - relay/proxy wrappers capitalizing "Connection refused"
//       fell through to the blind-spot fallback.

import (
	"errors"
	"strings"
	"testing"
)

// V1: a gateway error with a strippable prefix and a credential pair in the
// remainder must be redacted on the prefix-strip path.
func TestIssue3077_PrefixStripBranchSanitizes(t *testing.T) {
	raw := "gemini chat: 403 forbidden: https://proxy.example/v1?api_key=AIzaSyA1234567890abcdefghj"
	got := UserFacingErrorLang(errors.New(raw), "en")
	if !strings.Contains(got, "Request failed") {
		t.Fatalf("expected prefix-strip fallback wording, got: %q", got)
	}
	if strings.Contains(got, "AIzaSyA1234567890abcdefghj") {
		t.Fatalf("credential leaked through prefix-strip branch (V1): %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker, got: %q", got)
	}
	// Non-credential text must survive the strip path.
	if !strings.Contains(got, "403 forbidden") {
		t.Fatalf("diagnostic text lost by sanitization: %q", got)
	}
}

// V1 companion: clean prefix-stripped messages are unchanged (no marker).
func TestIssue3077_PrefixStripCleanMessageUnchanged(t *testing.T) {
	got := UserFacingErrorLang(errors.New("openai chat: model overloaded"), "en")
	if got != "Request failed: model overloaded" {
		t.Fatalf("clean message altered: %q", got)
	}
}

// V2: capitalized "Connection refused" (relay wrapper) must hit the network
// branch, not the blind-spot fallback.
func TestIssue3077_ConnectionRefusedCaseInsensitive(t *testing.T) {
	got := UserFacingErrorLang(errors.New("relay upstream: Connection refused"), "en")
	if !strings.Contains(got, "Cannot connect to the API server") {
		t.Fatalf("capitalized Connection refused fell to blind spot (V2): %q", got)
	}
	// Original lowercase phrasing still matches.
	got = UserFacingErrorLang(errors.New("dial tcp: connection refused"), "en")
	if !strings.Contains(got, "Cannot connect to the API server") {
		t.Fatalf("lowercase connection refused regressed (V2): %q", got)
	}
	// Mixed case "No Such Host" too.
	got = UserFacingErrorLang(errors.New("lookup api.example.com: No Such Host"), "en")
	if !strings.Contains(got, "Cannot connect to the API server") {
		t.Fatalf("mixed-case no such host missed (V2): %q", got)
	}
}
