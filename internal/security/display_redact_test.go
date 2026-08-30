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

// #1306: seven formats lived only in the detection layer (secretdetect.go)
// - display redaction drifted for the third time (#1289 pattern). Each must
// now be masked in RedactForDisplay output.
//
// Tokens are constructed at runtime: the shapes must be realistic enough to
// exercise the regexes, but literal secrets in source trip GitHub push
// protection (this very test was blocked once).
func TestRedactForDisplayCoversDetectionLayerFormats(t *testing.T) {
	rep := func(s string, n int) string { return strings.Repeat(s, n) }
	cases := []struct {
		name  string
		input string
	}{
		{"npm", "token npm_" + rep("aB1", 12) + " end"}, // 36 chars
		{"pypi", "pypi-AgEIcHlwaW5p" + rep("aB1", 18)},  // >=50 chars
		{"docker", "dckr_pat_" + rep("aB1", 9)},         // 27 chars
		{"twilio", "auth SK" + rep("a1", 16)},           // 32 hex
		{"postgres", "postgres://admin:" + rep("x9", 4) + "@db.example.com:5432/prod"},
		{"aws_secret", "aws_secret_access_key = " + rep("aB1", 13) + "a"}, // 40 chars
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
