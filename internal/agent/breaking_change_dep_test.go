package agent

import (
	"strings"
	"testing"
)

func TestCheckBreakingChangeDep_GoModV1ToV2(t *testing.T) {
	old := `module example.com/myapp

go 1.21

require (
	github.com/gin-gonic/gin v1.9.0
	github.com/spf13/viper v1.15.0
)
`
	new := `module example.com/myapp

go 1.21

require (
	github.com/gin-gonic/gin v2.0.0
	github.com/spf13/viper v1.16.0
)
`
	warnings := checkBreakingChangeDep("go.mod", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for gin v1->v2 bump, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "gin") && strings.Contains(w, "v1 -> v2") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected gin v1->v2 warning, got: %v", warnings)
	}
}

func TestCheckBreakingChangeDep_NoMajorBump(t *testing.T) {
	old := `module example.com/myapp

go 1.21

require github.com/spf13/viper v1.15.0
`
	new := `module example.com/myapp

go 1.21

require github.com/spf13/viper v1.16.0
`
	warnings := checkBreakingChangeDep("go.mod", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for minor bump, got: %v", warnings)
	}
}

func TestCheckBreakingChangeDep_NpmReact17To18(t *testing.T) {
	old := `{
  "dependencies": {
    "react": "17.0.2",
    "axios": "0.21.1"
  }
}`
	new := `{
  "dependencies": {
    "react": "18.2.0",
    "axios": "0.21.1"
  }
}`
	warnings := checkBreakingChangeDep("package.json", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for react v17->v18 bump")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "react") && strings.Contains(w, "createRoot") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected react migration guidance with createRoot, got: %v", warnings)
	}
}

func TestCheckBreakingChangeDep_GenericGoModule(t *testing.T) {
	old := `module example.com/myapp

go 1.21

require github.com/some/unknown v1.5.0
`
	new := `module example.com/myapp

go 1.21

require github.com/some/unknown v2.1.0
`
	warnings := checkBreakingChangeDep("go.mod", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	// Should include generic Go import path guidance
	if !strings.Contains(warnings[0], "/v2") {
		t.Fatalf("expected Go import path guidance with /v2 suffix, got: %s", warnings[0])
	}
}

func TestCheckBreakingChangeDep_NonManifestFile(t *testing.T) {
	warnings := checkBreakingChangeDep("main.go", "v1", "v2")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-manifest file, got: %v", warnings)
	}
}

func TestCheckBreakingChangeDep_DowngradeNoWarning(t *testing.T) {
	old := `{
  "dependencies": {
    "react": "18.2.0"
  }
}`
	new := `{
  "dependencies": {
    "react": "17.0.2"
  }
}`
	warnings := checkBreakingChangeDep("package.json", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for downgrade, got: %v", warnings)
	}
}

func TestCheckBreakingChangeDep_PythonDjango3To4(t *testing.T) {
	old := `django==3.2.0
flask==2.0.0
`
	new := `django==4.2.0
flask==2.0.0
`
	warnings := checkBreakingChangeDep("requirements.txt", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for django v3->v4 bump")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "django") && strings.Contains(w, "async") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected django migration guidance, got: %v", warnings)
	}
}

func TestCheckBreakingChangeDep_RustCargoBump(t *testing.T) {
	old := `[dependencies]
tokio = "0.2.0"
serde = "1.0.0"
`
	new := `[dependencies]
tokio = "1.0.0"
serde = "1.0.0"
`
	warnings := checkBreakingChangeDep("Cargo.toml", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for tokio v0->v1 bump")
	}
}

func TestExtractMajorVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"v1.2.3", 1},
		{"2.0.0", 2},
		{"v0.5.1", 0},
		{"18.2.0", 18},
		{"4.17.21", 4},
		{"latest", -1},
		{"", -1},
		{">=3.2.0", 3},
		{"^5.0.0", 5},
	}
	for _, tt := range tests {
		got := extractMajorVersion(tt.input)
		if got != tt.expected {
			t.Errorf("extractMajorVersion(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestCheckBreakingChangeDepAsString(t *testing.T) {
	old := `module example.com/myapp
go 1.21
require github.com/gin-gonic/gin v1.9.0
`
	new := `module example.com/myapp
go 1.21
require github.com/gin-gonic/gin v2.0.0
`
	result := checkBreakingChangeDepAsString("go.mod", old, new)
	if result == "" {
		t.Fatal("expected non-empty warning string")
	}
	if !strings.Contains(result, "Major Version Bump") {
		t.Fatalf("expected 'Major Version Bump' in result, got: %s", result)
	}
}
