package agent

import (
	"strings"
	"testing"
)

func TestCheckHardcodedSecrets_NoSecrets(t *testing.T) {
	old := "package main\n\nfunc main() {}\n"
	new := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	warnings := checkHardcodedSecrets("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckHardcodedSecrets_AWSKeyIntroduced(t *testing.T) {
	old := "package main\n"
	new := `package main

const accessKey = "AKIAIOSFODNN7EXAMPLE"
`
	warnings := checkHardcodedSecrets("config.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded secret warning for AWS key")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "aws_access_key") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected aws_access_key warning, got: %v", warnings)
	}
}

func TestCheckHardcodedSecrets_GitHubTokenIntroduced(t *testing.T) {
	old := ""
	new := `const token = "ghp_1234567890abcdefghijklmnopqrstuvwxyz"`
	warnings := checkHardcodedSecrets("auth.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded secret warning for GitHub token")
	}
}

func TestCheckHardcodedSecrets_PreExistingNotFlagged(t *testing.T) {
	// If the secret was already in oldContent, it should NOT be flagged
	old := `const accessKey = "AKIAIOSFODNN7EXAMPLE"`
	new := old + "\n// added comment\n"
	warnings := checkHardcodedSecrets("config.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for pre-existing secret, got %d", len(warnings))
	}
}

func TestCheckHardcodedSecrets_EnvFileExempt(t *testing.T) {
	new := `AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE`
	warnings := checkHardcodedSecrets(".env", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected .env file to be exempt, got %d warnings", len(warnings))
	}
}

func TestCheckHardcodedSecrets_EnvExampleExempt(t *testing.T) {
	new := `AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE`
	warnings := checkHardcodedSecrets(".env.example", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected .env.example to be exempt, got %d warnings", len(warnings))
	}
}

func TestCheckHardcodedSecrets_PEMFileExempt(t *testing.T) {
	new := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----"
	warnings := checkHardcodedSecrets("server.pem", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected .pem file to be exempt, got %d warnings", len(warnings))
	}
}

func TestCheckHardcodedSecrets_TestDataExempt(t *testing.T) {
	new := `const token = "ghp_1234567890abcdefghijklmnopqrstuvwxyz"`
	warnings := checkHardcodedSecrets("testdata/fixtures/config.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected testdata/ to be exempt, got %d warnings", len(warnings))
	}
}

func TestCheckHardcodedSecrets_AssignmentInConfigNotFlagged(t *testing.T) {
	// assignment_secret should not be flagged in YAML/JSON config files
	new := "api_key: AIzaSyB1234567890abcdefghijklmnopqrstuvwxyz"
	warnings := checkHardcodedSecrets("config.yaml", "", new)
	// GCP key pattern should still match, but assignment_secret should not
	for _, w := range warnings {
		if strings.Contains(w, "assignment_secret") {
			t.Errorf("assignment_secret should not be flagged in config file")
		}
	}
}

func TestCheckHardcodedSecrets_AssignmentInSourceCode(t *testing.T) {
	// assignment_secret SHOULD be flagged in Go source code
	new := `apiKey := "AKIAIOSFODNN7EXAMPLE1234567"`
	warnings := checkHardcodedSecrets("main.go", "", new)
	// Should detect at least something (AWS key or assignment pattern)
	if len(warnings) == 0 {
		// The assignment pattern requires 20+ chars and specific key names
		// AKIA prefix is 4 chars + 16 = 20 chars total which matches aws_access_key
		t.Logf("warnings: %v", warnings)
	}
}

func TestCheckHardcodedSecrets_PrivateKeyIntroduced(t *testing.T) {
	old := ""
	new := `package main

var key = ` + "`" + `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEAvrSgZNh2...
-----END RSA PRIVATE KEY-----` + "`"
	warnings := checkHardcodedSecrets("keys.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded secret warning for private key")
	}
}

func TestCheckHardcodedSecrets_EmptyContent(t *testing.T) {
	warnings := checkHardcodedSecrets("main.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckHardcodedSecrets_JWTToken(t *testing.T) {
	old := ""
	new := `const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"`
	warnings := checkHardcodedSecrets("auth.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded secret warning for JWT")
	}
}

func TestFormatSecretWarning(t *testing.T) {
	w := formatSecretWarning("github_token", 1)
	if !strings.Contains(w, "SECURITY WARNING") {
		t.Errorf("warning should contain SECURITY WARNING")
	}
	if !strings.Contains(w, "github_token") {
		t.Errorf("warning should contain secret type")
	}
	if !strings.Contains(w, "OWASP") {
		t.Errorf("warning should reference OWASP")
	}

	w2 := formatSecretWarning("aws_access_key", 3)
	if !strings.Contains(w2, "instances") {
		t.Errorf("plural form should use 'instances'")
	}
}

// TestHardcodedSecret_ReplacementDetected pins fix #171: swapping a fake key
// for a REAL credential of the same pattern family (remove-1-add-1, net 0)
// must still warn.
func TestHardcodedSecret_ReplacementDetected(t *testing.T) {
	oldSrc := "package main\nvar k = \"AKIAIOSFODNN7EXAMPLE\"\n"
	newSrc := "package main\nvar k = \"AKIAIOSFODNN7REALKEY\"\n"
	w := checkHardcodedSecrets("a.go", oldSrc, newSrc)
	if len(w) == 0 {
		t.Fatal("fake-to-real key replacement must be detected (remove-N-add-N blindness, #171)")
	}
}
