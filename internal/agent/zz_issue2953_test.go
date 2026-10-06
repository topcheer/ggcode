package agent

import (
	"strings"
	"testing"
)

// #2953: isSensitiveFieldName must not flag token COUNTING/QOTA fields
// (MaxTokens, TokenCount, Tokens, TokenUsage) as credential leaks, while
// genuine credential names (BotToken, AccessToken, TokenSecret) stay flagged.

func TestIssue2953TokenCounterFieldsNotSensitive(t *testing.T) {
	benign := []string{
		"MaxTokens", "Tokens", "TokenCount", "TokenUsage", "TokenLimit",
		"TokenBudget", "TokensLeft", "TokenPrice", "PromptTokens", "OutputTokens",
	}
	for _, name := range benign {
		if isSensitiveFieldName(name) {
			t.Errorf("isSensitiveFieldName(%q) = true, want false (counter/quota semantics, #2953)", name)
		}
	}
}

func TestIssue2953CredentialTokenFieldsStillSensitive(t *testing.T) {
	credentials := []string{"BotToken", "APIAccessToken", "TokenSecret", "RefreshToken", "ApiToken"}
	for _, name := range credentials {
		if !isSensitiveFieldName(name) {
			t.Errorf("isSensitiveFieldName(%q) = false, want true (credential semantics must be preserved)", name)
		}
	}
}

func TestIssue2953NoWarningForCounterStructFields(t *testing.T) {
	// The exact reproducer from the issue: three counter fields, zero warnings.
	src := `package p

type Request struct {
	MaxTokens  int      ` + "`json:\"max_tokens,omitempty\"`" + `
	Tokens     []string ` + "`json:\"tokens\"`" + `
	TokenCount int
}
`
	warnings := checkSensitiveJSONExposure("req.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("checkSensitiveJSONExposure returned %d warnings for counter fields, want 0: %v", len(warnings), warnings)
	}
}

func TestIssue2953WarningStillFiresForCredentialField(t *testing.T) {
	// Regression guard: a genuine credential field must still warn.
	src := `package p

type Creds struct {
	BotToken string ` + "`json:\"bot_token\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("creds.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("checkSensitiveJSONExposure returned no warning for credential field BotToken, want 1")
	}
	if !strings.Contains(warnings[0], "bot_token") && !strings.Contains(warnings[0], "BotToken") {
		t.Errorf("warning does not reference the credential field: %q", warnings[0])
	}
}
