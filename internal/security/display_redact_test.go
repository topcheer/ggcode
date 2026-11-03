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

func TestRedactForDisplay_GitHubFineGrainedPAT(t *testing.T) {
	token := "github_pat_" + strings.Repeat("A1b2C3d4E5f6G7h8I9j0", 4) + "Zz"
	if len(token) != len("github_pat_")+82 {
		t.Fatalf("test fixture malformed: %d chars", len(token))
	}
	out := RedactForDisplay("token: " + token + " here")
	if strings.Contains(out, token) {
		t.Fatalf("fine-grained PAT leaked: %q", out)
	}

	classic := "ghp_" + strings.Repeat("x", 40)
	out = RedactForDisplay("key " + classic)
	if strings.Contains(out, classic) {
		t.Fatalf("classic PAT leaked: %q", out)
	}

	short := "github_pat_" + strings.Repeat("a", 40)
	out = RedactForDisplay("x " + short)
	if !strings.Contains(out, short) {
		t.Fatalf("short fragment wrongly masked: %q", out)
	}
}

func TestRedactForDisplayCoversDetectionLayerFormats(t *testing.T) {
	rep := func(s string, n int) string { return strings.Repeat(s, n) }
	cases := []struct {
		name  string
		input string
	}{
		{"npm", "token npm_" + rep("aB1", 12) + " end"},
		{"pypi", "pypi-AgEIcHlwaW5p" + rep("aB1", 18)},
		{"docker", "dckr_pat_" + rep("aB1", 9)},
		{"twilio", "auth SK" + rep("a1", 16)},
		{"postgres", "postgres://admin:" + rep("x9", 4) + "@db.example.com:5432/prod"},
		{"aws_secret", "aws_secret_access_key = " + rep("aB1", 13) + "a"},
		{"azure", "DefaultEndpointsProtocol=https;AccountName=x;AccountKey=" + rep("aB1", 17) + "==;EndpointSuffix=core.windows.net"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactForDisplay(tc.input)
			if got == tc.input {
				t.Errorf("secret not masked: %q", got)
			}
			if !strings.Contains(got, "***") {
				t.Errorf("expected masked output, got %q", got)
			}
		})
	}
}

func TestRedactTwoGroupNoPlaintextEcho(t *testing.T) {
	const awsSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	out := RedactForDisplay("AWS_SECRET_ACCESS_KEY=" + awsSecret)
	if strings.Contains(out, awsSecret) {
		t.Fatalf("plaintext secret re-echoed in redacted output: %q", out)
	}
	const azSecret = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUV0123456789+ab="
	out2 := RedactForDisplay("AccountKey=" + azSecret)
	if strings.Contains(out2, azSecret) {
		t.Fatalf("plaintext azure key re-echoed: %q", out2)
	}
}

func TestRedactBarePKCS8PrivateKey(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQC7\n-----END PRIVATE KEY-----"
	out := RedactForDisplay(pem)
	if strings.Contains(out, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQC7") {
		t.Fatalf("bare PKCS#8 body leaked: %q", out)
	}
}
