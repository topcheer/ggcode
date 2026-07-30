package agent

import (
	"strings"
	"testing"
)

func TestRedactSecrets_AWSAccessKey(t *testing.T) {
	content := `config:
  access_key_id: AKIAFAKEEXAMPLEKEY00
  region: us-east-1`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "AKIAFAKEEXAMPLEKEY00") {
		t.Errorf("AWS access key should be redacted, got: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:aws_access_key]") {
		t.Errorf("should contain redaction marker, got: %s", result)
	}
}

func TestRedactSecrets_AWSSecretKey(t *testing.T) {
	content := `aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY") {
		t.Errorf("AWS secret key value should be redacted, got: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:aws_secret_key]") {
		t.Errorf("should contain redaction marker, got: %s", result)
	}
}

func TestRedactSecrets_GitHubToken(t *testing.T) {
	content := `GITHUB_TOKEN=ghp_FAKE_TOKEN_FAKE_TOKEN_FAKE`
	result := redactSecrets("run_command", content)
	if strings.Contains(result, "ghp_FAKE_TOKEN_FAKE_TOKEN_FAKE") {
		t.Errorf("GitHub token should be redacted, got: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:github_token]") {
		t.Errorf("should contain github_token redaction marker")
	}
}

func TestRedactSecrets_SlackToken(t *testing.T) {
	content := `token: xoxb-FAKE-SLACK-TOKEN-FAKE-FAKE1234`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "xoxb-") {
		t.Errorf("Slack token should be redacted, got: %s", result)
	}
}

func TestRedactSecrets_PrivateKey(t *testing.T) {
	content := `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyz
-----END RSA PRIVATE KEY-----`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "MIIEpAIBAAKCAQEA") {
		t.Errorf("Private key content should be redacted")
	}
	if !strings.Contains(result, "[REDACTED:private_key]") {
		t.Errorf("should contain private_key redaction marker")
	}
}

func TestRedactSecrets_BearerToken(t *testing.T) {
	content := `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload1234567890abcdef`
	result := redactSecrets("run_command", content)
	if strings.Contains(result, "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload1234567890abcdef") {
		t.Errorf("Bearer token value should be redacted, got: %s", result)
	}
}

func TestRedactSecrets_AssignmentSecret(t *testing.T) {
	content := `api_key: "sk-1234567890abcdefghij123"`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "sk-1234567890abcdefghij123") {
		t.Errorf("API key value should be redacted, got: %s", result)
	}
}

func TestRedactSecrets_AssignmentSecretPreservesKey(t *testing.T) {
	content := `API_KEY=abcdefghijklmnopqrstuvwx1234567890`
	result := redactSecrets("read_file", content)
	if !strings.Contains(result, "API_KEY=") {
		t.Errorf("key name should be preserved, got: %s", result)
	}
	if strings.Contains(result, "abcdefghijklmnopqrstuvwx1234567890") {
		t.Errorf("secret value should be masked, got: %s", result)
	}
}

func TestRedactSecrets_JWT(t *testing.T) {
	content := `token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c`
	result := redactSecrets("grep", content)
	if strings.Contains(result, "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c") {
		t.Errorf("JWT should be redacted, got: %s", result)
	}
}

func TestRedactSecrets_GCPAPIKey(t *testing.T) {
	content := `key: AIzaFAKE_GOOGLE_API_KEY_1234567`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "AIzaFAKE_GOOGLE_API_KEY_1234567") {
		t.Errorf("GCP API key should be redacted, got: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:gcp_api_key]") {
		t.Errorf("should contain gcp_api_key redaction marker, got: %s", result)
	}
}

func TestRedactSecrets_StripeKey(t *testing.T) {
	content := `stripe_key: sk_test_FAKEKEY_FAKEKEY_FAKE`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "sk_test_FAKEKEY_FAKEKEY_FAKE") {
		t.Errorf("Stripe key should be redacted, got: %s", result)
	}
}

func TestRedactSecrets_NoFalsePositive(t *testing.T) {
	// Short values that are NOT secrets should not be flagged
	content := `port: 8080
host: localhost
debug: true
timeout: 30s
name: my-app
version: 1.0.0
url: http://example.com:3000/api/v1`
	result := redactSecrets("read_file", content)
	if result != content {
		t.Errorf("non-secret content should not be modified, got: %s", result)
	}
}

func TestRedactSecrets_NoFalsePositiveOnHexIDs(t *testing.T) {
	// Git commit hashes, UUIDs, and other non-secret hex strings
	content := `commit: abc123def456789012345678901234567890abcd
uuid: 550e8400-e29b-41d4-a716-446655440000
sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`
	result := redactSecrets("read_file", content)
	// These should NOT be redacted since they don't match secret patterns
	if !strings.Contains(result, "550e8400") {
		t.Errorf("UUID should not be redacted, got: %s", result)
	}
}

func TestRedactSecrets_SkipsNonContentTools(t *testing.T) {
	// edit_file is not in externalContentTools — should not be scanned
	content := `api_key: "sk-test1234567890abcdefghij"`
	result := redactSecrets("edit_file", content)
	if result != content {
		t.Errorf("non-content tool should not be redacted, got: %s", result)
	}
}

func TestRedactSecrets_MCPTool(t *testing.T) {
	content := `GITHUB_TOKEN=ghp_FAKE_TOKEN_FAKE_TOKEN_FAKE`
	result := redactSecrets("mcp__github__get_file", content)
	if strings.Contains(result, "ghp_FAKE_TOKEN_FAKE_TOKEN_FAKE") {
		t.Errorf("MCP tool result should be redacted, got: %s", result)
	}
}

func TestRedactSecrets_MultipleSecrets(t *testing.T) {
	content := `AWS_KEY=AKIAFAKEEXAMPLEKEY00
GH_TOKEN=ghp_FAKE_TOKEN_FAKE_TOKEN_FAKE`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "AKIAFAKEEXAMPLEKEY00") {
		t.Errorf("AWS key should be redacted")
	}
	if strings.Contains(result, "ghp_") {
		t.Errorf("GitHub token should be redacted")
	}
	if !strings.Contains(result, "[REDACTED:aws_access_key]") {
		t.Errorf("should contain aws redaction marker")
	}
	if !strings.Contains(result, "[REDACTED:github_token]") {
		t.Errorf("should contain github redaction marker")
	}
}

func TestRedactSecrets_EmptyContent(t *testing.T) {
	result := redactSecrets("read_file", "")
	if result != "" {
		t.Errorf("empty content should return empty, got: %s", result)
	}
}

func TestRedactSecrets_PreservesContext(t *testing.T) {
	// The key name and surrounding structure should be preserved
	content := `database:
  host: db.example.com
  port: 5432
  password: supersecrettoken1234567890abcd`
	result := redactSecrets("read_file", content)
	if !strings.Contains(result, "database:") {
		t.Errorf("database section should be preserved")
	}
	if !strings.Contains(result, "host: db.example.com") {
		t.Errorf("host should be preserved")
	}
	if !strings.Contains(result, "port: 5432") {
		t.Errorf("port should be preserved")
	}
	if strings.Contains(result, "supersecrettoken1234567890abcd") {
		t.Errorf("password should be redacted")
	}
}

func TestRedactSecrets_GitLabToken(t *testing.T) {
	content := `gitlab_token: glpat-1234567890abcdefghij`
	result := redactSecrets("read_file", content)
	if strings.Contains(result, "glpat-") {
		t.Errorf("GitLab token should be redacted, got: %s", result)
	}
}
