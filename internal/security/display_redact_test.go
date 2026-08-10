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
