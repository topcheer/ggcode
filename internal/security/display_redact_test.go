package security

import (
	"strings"
	"testing"
)

func TestRedactForDisplay_OpenAIKey(t *testing.T) {
	key := "sk-" + strings.Repeat("a", 30)
	content := "api_key: " + key
	result := RedactForDisplay(content)
	if strings.Contains(result, key) {
		t.Errorf("OpenAI key should be masked, got: %s", result)
	}
	if !strings.Contains(result, "****") && !strings.Contains(result, "sk-a") {
		// Should show partial masking with asterisks
		t.Errorf("Expected partial masking, got: %s", result)
	}
}

func TestRedactForDisplay_PreservesShortText(t *testing.T) {
	result := RedactForDisplay("hello world")
	if result != "hello world" {
		t.Errorf("Short text should be unchanged, got: %s", result)
	}
}

func TestRedactForDisplay_AssignmentPattern(t *testing.T) {
	val := strings.Repeat("x", 30)
	content := `api_key = "` + val + `"`
	result := RedactForDisplay(content)
	if strings.Contains(result, val) {
		t.Errorf("Assignment secret should be masked, got: %s", result)
	}
}

func TestRedactForDisplay_AWSKey(t *testing.T) {
	key := "AKIA" + strings.Repeat("A", 16)
	content := "AWS_ACCESS_KEY=" + key
	result := RedactForDisplay(content)
	if strings.Contains(result, key) {
		t.Errorf("AWS key should be masked, got: %s", result)
	}
}

func TestHasSecretPattern(t *testing.T) {
	if HasSecretPattern("hello world") {
		t.Error("Plain text should not trigger secret pattern")
	}
	key := "sk-" + strings.Repeat("a", 30)
	if !HasSecretPattern("key: " + key) {
		t.Error("OpenAI key should trigger secret pattern")
	}
}

// #1289: bare fine-grained PAT text must be masked. The detection layer
// (secretdetect.go #793) got the github_pat_ pattern but this display list
// had drifted - gh[pousr]_ does not match github_pat_, so the token was
// pushed verbatim to IM / TUI / desktop session exports.
func TestRedactForDisplay_GitHubFineGrainedPAT(t *testing.T) {
	token := "github_pat_" + strings.Repeat("A1b2C3d4E5f6G7h8I9j0", 4) + "Zz" // 82 chars after prefix
	if len(token) != len("github_pat_")+82 {
		t.Fatalf("test fixture malformed: %d chars", len(token))
	}
	out := RedactForDisplay("token: " + token + " here")
	if strings.Contains(out, token) {
		t.Fatalf("fine-grained PAT leaked: %q", out)
	}
	// Classic forms still masked (no regression).
	classic := "ghp_" + strings.Repeat("x", 40)
	out = RedactForDisplay("key " + classic)
	if strings.Contains(out, classic) {
		t.Fatalf("classic PAT leaked: %q", out)
	}
	// Too-short github_pat_ fragments are NOT masked (precision).
	short := "github_pat_" + strings.Repeat("a", 40)
	out = RedactForDisplay("x " + short)
	if !strings.Contains(out, short) {
		t.Fatalf("short fragment wrongly masked: %q", out)
	}
}
