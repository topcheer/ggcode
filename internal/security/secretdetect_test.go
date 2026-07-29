package security

import (
	"testing"
)

func TestScanForSecrets_AWSAccessKey(t *testing.T) {
	content := `config:
  access_key: AKIAIOSFODNN7EXAMPLE
  region: us-east-1
`
	findings := ScanForSecrets("config.yaml", content)
	if len(findings) == 0 {
		t.Fatal("expected AWS access key detection")
	}
	found := false
	for _, f := range findings {
		if f.PatternID == "aws_access_key" {
			found = true
			if f.Severity != "high" {
				t.Errorf("expected high severity, got %s", f.Severity)
			}
		}
	}
	if !found {
		t.Error("AWS access key pattern not matched")
	}
}

func TestScanForSecrets_GitHubPAT(t *testing.T) {
	content := `token: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB`
	findings := ScanForSecrets("config.yml", content)
	if len(findings) == 0 {
		t.Fatal("expected GitHub PAT detection")
	}
	found := false
	for _, f := range findings {
		if f.PatternID == "github_pat" {
			found = true
		}
	}
	if !found {
		t.Error("GitHub PAT pattern not matched")
	}
}

func TestScanForSecrets_PrivateKey(t *testing.T) {
	content := `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyz
-----END RSA PRIVATE KEY-----`
	findings := ScanForSecrets("id_rsa", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "private_key_block" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected private key block detection")
	}
}

func TestScanForSecrets_OpenAIKey(t *testing.T) {
	content := `openai_api_key = "sk-proj1234567890abcdefghij"`
	findings := ScanForSecrets("env.go", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "openai_api_key" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected OpenAI API key detection")
	}
}

func TestScanForSecrets_DatabasePassword(t *testing.T) {
	content := `DATABASE_URL=postgres://user:supersecretpass12345@db.example.com:5432/mydb`
	findings := ScanForSecrets("config.env", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "db_conn_password" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected database connection password detection")
	}
}

func TestScanForSecrets_GenericAssignment(t *testing.T) {
	content := `const apiKey = "sk-1234567890abcdef1234567890abcdef"`
	findings := ScanForSecrets("app.ts", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "generic_api_key_assignment" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected generic API key assignment detection")
	}
}

func TestScanForSecrets_SkipsPlaceholders(t *testing.T) {
	content := `api_key = "your-api-key-here"
secret = "changeme"
token = "xxxx"`
	findings := ScanForSecrets("config.go", content)
	// These should all be filtered as placeholders
	for _, f := range findings {
		if f.PatternID == "generic_api_key_assignment" {
			t.Errorf("placeholder value should not be flagged: %s", f.Match)
		}
	}
}

func TestScanForSecrets_SkipsTestFiles(t *testing.T) {
	content := `token = "sk-realsecretkey1234567890abcdef"`
	findings := ScanForSecrets("config_test.go", content)
	if len(findings) != 0 {
		t.Fatalf("test files should be skipped, got %d findings", len(findings))
	}
}

func TestScanForSecrets_SkipsFixtures(t *testing.T) {
	content := `token = "sk-realsecretkey1234567890abcdef"`
	findings := ScanForSecrets("testdata/config.json", content)
	if len(findings) != 0 {
		t.Fatalf("testdata files should be skipped, got %d findings", len(findings))
	}
}

func TestScanForSecrets_SkipsFixturesWindowsPath(t *testing.T) {
	content := `token = "sk-realsecretkey1234567890abcdef"`
	findings := ScanForSecrets(`testdata\config.json`, content)
	if len(findings) != 0 {
		t.Fatalf("Windows testdata paths should be skipped, got %d findings", len(findings))
	}
}

func TestScanForSecrets_OpenAIProjectKey(t *testing.T) {
	content := `OPENAI_API_KEY=sk-proj-AbCdEfGhIjKlMnOpQrStUvWx`
	findings := ScanForSecrets("config.env", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "openai_api_key" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected sk-proj- OpenAI key detection")
	}
}

func TestScanForSecrets_SkipsExamples(t *testing.T) {
	content := `api_key = "sk-realsecretkey1234567890abcdef"`
	findings := ScanForSecrets(".env.example", content)
	if len(findings) != 0 {
		t.Fatalf("example files should be skipped, got %d findings", len(findings))
	}
}

func TestScanForSecrets_NoSecrets(t *testing.T) {
	content := `package main

func main() {
	fmt.Println("hello world")
}`
	findings := ScanForSecrets("main.go", content)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for normal code, got %d", len(findings))
	}
}

func TestScanForSecrets_JWT(t *testing.T) {
	content := `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUPCjhL3Ss`
	findings := ScanForSecrets("request.go", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "jwt_token" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected JWT token detection")
	}
}

func TestScanForSecrets_MultipleFindings(t *testing.T) {
	content := `aws_key: AKIAIOSFODNN7EXAMPLE
gh_token: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB
normal_var: hello`
	findings := ScanForSecrets("multi.yaml", content)
	if len(findings) < 2 {
		t.Fatalf("expected at least 2 findings, got %d", len(findings))
	}
}

func TestScanForSecrets_SlackToken(t *testing.T) {
	// Token assembled from parts so the fixture does not trip GitHub push
	// protection (it pattern-matches real Slack tokens in committed blobs).
	content := `slack_bot_token: ` + "xoxb-" + "1234567890-1234567890-abcdefabcdefabcdefabcdefabcdef"
	findings := ScanForSecrets("slack.go", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "slack_token" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected Slack token detection")
	}
}

func TestScanForSecrets_GoogleAPIKey(t *testing.T) {
	content := `google_api_key: AIzaSyD-9tSrke72PouQMnMX-a7eZSW0jkFMBWY`
	findings := ScanForSecrets("config.go", content)
	found := false
	for _, f := range findings {
		if f.PatternID == "gcp_api_key" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected Google API key detection")
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"short", "*****"},                                                 // len 5 → all masked
		{"1234567890ab", "************"},                                   // len 12 → all masked
		{"1234567890abc", "1234*****0abc"},                                 // len 13 → first4 + mask5 + last4
		{"sk-1234567890abcdefghijklmnop", "sk-1*********************mnop"}, // len 29
	}
	for _, tt := range tests {
		got := maskSecret(tt.input)
		if got != tt.expected {
			t.Errorf("maskSecret(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsPlaceholder(t *testing.T) {
	placeholders := []string{
		"your-api-key", "YOUR_API_KEY", "changeme", "placeholder",
		"xxxx", "test123", "example-key", "fake-token",
	}
	for _, p := range placeholders {
		if !isPlaceholder(p) {
			t.Errorf("isPlaceholder(%q) = false, want true", p)
		}
	}

	reals := []string{
		"sk-proj1234567890abcdef", "ghp_1234567890abcdefghijklmnopqrstuvwxyzAB",
		"AKIAIOSFODNN7REALKEY01",
	}
	for _, r := range reals {
		if isPlaceholder(r) {
			t.Errorf("isPlaceholder(%q) = true, want false", r)
		}
	}
}

func TestFormatWarnings_Empty(t *testing.T) {
	result := FormatWarnings(nil)
	if result != "" {
		t.Errorf("expected empty string for no findings, got %q", result)
	}
}

func TestFormatWarnings_WithFindings(t *testing.T) {
	findings := []Finding{
		{PatternID: "aws_access_key", Name: "AWS Access Key ID", Severity: "high", Line: 5, Match: "AKIA****MPLE"},
	}
	result := FormatWarnings(findings)
	if result == "" {
		t.Fatal("expected non-empty warning string")
	}
	if !contains(result, "SECURITY WARNING") {
		t.Error("expected SECURITY WARNING header")
	}
	if !contains(result, "AWS Access Key ID") {
		t.Error("expected finding name in output")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
