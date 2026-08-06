package agent

import (
	"testing"
)

func TestCheckSensitiveJSONExposure_PasswordField(t *testing.T) {
	src := `package main

type User struct {
	Name     string ` + "`json:\"name\"`" + `
	Password string ` + "`json:\"password\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !contains(warnings[0], "password") {
		t.Errorf("warning should mention password: %s", warnings[0])
	}
}

func TestCheckSensitiveJSONExposure_ExcludedField(t *testing.T) {
	src := `package main

type User struct {
	Name     string ` + "`json:\"name\"`" + `
	Password string ` + "`json:\"-\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for excluded field, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_NoJSONTag(t *testing.T) {
	src := `package main

type User struct {
	Name     string
	Password string
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for field without json tag, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_ApiKeyTag(t *testing.T) {
	src := `package main

type Config struct {
	Key string ` + "`json:\"api_key,omitempty\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for api_key tag, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_TokenInFieldName(t *testing.T) {
	src := `package main

type AuthResponse struct {
	AccessToken string ` + "`json:\"access_token\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for access_token, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_NonSensitiveField(t *testing.T) {
	src := `package main

type User struct {
	Name  string ` + "`json:\"name\"`" + `
	Email string ` + "`json:\"email\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for normal fields, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_SensitiveFieldNameButTagIsDash(t *testing.T) {
	src := `package main

type Credentials struct {
	Secret string ` + "`json:\"-\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_NonGoFile(t *testing.T) {
	warnings := checkSensitiveJSONExposure("test.py", "", "password = 'secret'")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckSensitiveJSONExposure_MultipleSensitiveFields(t *testing.T) {
	src := `package main

type Account struct {
	Password   string ` + "`json:\"password\"`" + `
	ApiToken   string ` + "`json:\"api_token\"`" + `
	SecretKey  string ` + "`json:\"secret_key\"`" + `
	Name       string ` + "`json:\"name\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_SensitiveGoNameButSafeJSONName(t *testing.T) {
	// If the Go field name is "PasswordHash" but json tag is "hash",
	// the Go field name is still sensitive.
	src := `package main

type User struct {
	PasswordHash string ` + "`json:\"hash\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for PasswordHash field, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSensitiveJSONExposure_MaxWarningsCap(t *testing.T) {
	src := `package main

type Data struct {
	Password    string ` + "`json:\"password\"`" + `
	Token       string ` + "`json:\"token\"`" + `
	Secret      string ` + "`json:\"secret\"`" + `
	ApiKey      string ` + "`json:\"api_key\"`" + `
	AccessToken string ` + "`json:\"access_token\"`" + `
}
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != sensitiveJSONMaxWarnings {
		t.Fatalf("expected %d warnings (cap), got %d", sensitiveJSONMaxWarnings, len(warnings))
	}
}

func TestCheckSensitiveJSONExposure_EmptyContent(t *testing.T) {
	warnings := checkSensitiveJSONExposure("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckSensitiveJSONExposure_InvalidGoSyntax(t *testing.T) {
	src := `package main
type Foo struct {
	Password string
`
	warnings := checkSensitiveJSONExposure("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for invalid Go, got %d", len(warnings))
	}
}

// contains and containsStr already defined in reflection_test.go
