package agent

import (
	"strings"
	"testing"
)

func TestCheckDependencyVulns_GoMod(t *testing.T) {
	// Adding a vulnerable go.mod dependency should trigger a warning.
	oldGoMod := `module example.com/test

go 1.21

require (
	github.com/gin-gonic/gin v1.9.0
)
`
	newGoMod := `module example.com/test

go 1.21

require (
	github.com/gin-gonic/gin v1.9.0
	golang.org/x/crypto v0.0.0-20200302210943-78000ba7a073 // indirect
)
`
	warnings := checkDependencyVulns("go.mod", oldGoMod, newGoMod)
	if len(warnings) == 0 {
		t.Fatal("expected vulnerability warning for vulnerable golang.org/x/crypto")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "golang.org/x/crypto") && strings.Contains(w, "CVE-2024-45337") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CVE-2024-45337 warning for crypto, got: %v", warnings)
	}
}

func TestCheckDependencyVulns_GoModSafeVersion(t *testing.T) {
	// Adding a safe version should NOT trigger a CVE warning, but should
	// show the general "consider scanning" reminder.
	oldGoMod := `module example.com/test

go 1.21
`
	newGoMod := `module example.com/test

go 1.21

require golang.org/x/crypto v0.31.0
`
	warnings := checkDependencyVulns("go.mod", oldGoMod, newGoMod)
	// Should NOT find CVE warning for crypto (it's patched)
	for _, w := range warnings {
		if strings.Contains(w, "CVE-2024-45337") {
			t.Fatalf("should not flag patched version, got: %s", w)
		}
	}
	// Should have the general reminder
	if len(warnings) == 0 {
		t.Fatal("expected dependency change reminder even for safe versions")
	}
}

func TestCheckDependencyVulns_NPM(t *testing.T) {
	oldPkg := `{
  "dependencies": {
    "express": "4.18.2"
  }
}
`
	newPkg := `{
  "dependencies": {
    "express": "4.18.2",
    "lodash": "4.17.20"
  }
}
`
	warnings := checkDependencyVulns("package.json", oldPkg, newPkg)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "lodash") && strings.Contains(w, "CVE-2021-23337") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected lodash CVE warning, got: %v", warnings)
	}
}

func TestCheckDependencyVulns_NPMCriticalEventStream(t *testing.T) {
	// event-stream is a permanently compromised package
	oldPkg := `{}`
	newPkg := `{
  "dependencies": {
    "event-stream": "3.3.4"
  }
}`
	warnings := checkDependencyVulns("package.json", oldPkg, newPkg)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "event-stream") && strings.Contains(w, "Critical") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected event-stream Critical warning, got: %v", warnings)
	}
}

func TestCheckDependencyVulns_PyPI(t *testing.T) {
	oldReq := `requests==2.31.0
`
	newReq := `requests==2.31.0
pyyaml==5.3.1
`
	warnings := checkDependencyVulns("requirements.txt", oldReq, newReq)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "pyyaml") && strings.Contains(w, "CVE-2020-1747") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pyyaml CVE warning, got: %v", warnings)
	}
}

func TestCheckDependencyVulns_Cargo(t *testing.T) {
	oldCargo := `[package]
name = "test"
version = "0.1.0"

[dependencies]
`
	newCargo := `[package]
name = "test"
version = "0.1.0"

[dependencies]
openssl = "0.10.30"
`
	warnings := checkDependencyVulns("Cargo.toml", oldCargo, newCargo)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "openssl") && strings.Contains(w, "CVE-2021-3711") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected openssl CVE warning, got: %v", warnings)
	}
}

func TestCheckDependencyVulns_NoChange(t *testing.T) {
	// If nothing changed, no warnings.
	content := `module example.com/test

require (
	golang.org/x/crypto v0.0.0-20200302210943-78000ba7a073
)
`
	warnings := checkDependencyVulns("go.mod", content, content)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when content unchanged, got: %v", warnings)
	}
}

func TestCheckDependencyVulns_NonManifestFile(t *testing.T) {
	// Should not run on non-manifest files.
	warnings := checkDependencyVulns("main.go", "", "package main\n")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-manifest file, got: %v", warnings)
	}
}

func TestCheckDependencyVulns_EmptyContent(t *testing.T) {
	warnings := checkDependencyVulns("go.mod", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"0.17.0", "0.31.0", -1},
		{"0.31.0", "0.17.0", 1},
		{"4.17.20", "4.17.21", -1},
		{"4.17.21", "4.17.21", 0},
		{"5.3.1", "5.4.0", -1},
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestStripVersionPrefix(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"v0.0.0-20200302210943-78000ba7a073", "0.0.0"},
		{"4.17.21", "4.17.21"},
	}
	for _, tt := range tests {
		got := stripVersionPrefix(tt.input)
		if got != tt.want {
			t.Errorf("stripVersionPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseGoMod(t *testing.T) {
	content := `module example.com/test

go 1.21

require (
	github.com/foo/bar v1.2.3
	golang.org/x/net v0.5.0
)

require example.com/single v0.1.0
`
	deps := parseGoMod(content)
	if deps["github.com/foo/bar"] != "v1.2.3" {
		t.Errorf("expected v1.2.3, got %q", deps["github.com/foo/bar"])
	}
	if deps["golang.org/x/net"] != "v0.5.0" {
		t.Errorf("expected v0.5.0, got %q", deps["golang.org/x/net"])
	}
	if deps["example.com/single"] != "v0.1.0" {
		t.Errorf("expected v0.1.0, got %q", deps["example.com/single"])
	}
}

func TestParsePackageJSON(t *testing.T) {
	content := `{
  "dependencies": {
    "lodash": "^4.17.20",
    "express": "~4.18.2"
  },
  "devDependencies": {
    "mocha": "10.0.0"
  }
}
`
	deps := parsePackageJSON(content)
	if deps["lodash"] != "4.17.20" {
		t.Errorf("expected 4.17.20 (prefix stripped), got %q", deps["lodash"])
	}
	if deps["express"] != "4.18.2" {
		t.Errorf("expected 4.18.2, got %q", deps["express"])
	}
	if deps["mocha"] != "10.0.0" {
		t.Errorf("expected 10.0.0, got %q", deps["mocha"])
	}
}

func TestParseRequirements(t *testing.T) {
	content := `# comment
requests==2.31.0
django>=4.2.0
# another comment
pyyaml~=5.3
`
	deps := parseRequirements(content)
	if deps["requests"] != "2.31.0" {
		t.Errorf("expected 2.31.0, got %q", deps["requests"])
	}
	if deps["django"] != "4.2.0" {
		t.Errorf("expected 4.2.0, got %q", deps["django"])
	}
}

func TestParseCargoToml(t *testing.T) {
	content := `[package]
name = "test"
version = "0.1.0"

[dependencies]
openssl = "0.10.30"
serde = "1.0"

[dev-dependencies]
proptest = "1.0"
`
	deps := parseCargoToml(content)
	if deps["openssl"] != "0.10.30" {
		t.Errorf("expected 0.10.30, got %q", deps["openssl"])
	}
	if deps["serde"] != "1.0" {
		t.Errorf("expected 1.0, got %q", deps["serde"])
	}
}

func TestCheckDependencyVulns_GoModUpgradeSafe(t *testing.T) {
	// Upgrading from vulnerable to patched should not trigger warning.
	oldGoMod := `module example.com/test

require golang.org/x/crypto v0.0.0-20200302210943-78000ba7a073
`
	newGoMod := `module example.com/test

require golang.org/x/crypto v0.31.0
`
	warnings := checkDependencyVulns("go.mod", oldGoMod, newGoMod)
	for _, w := range warnings {
		if strings.Contains(w, "CVE-2024-45337") {
			t.Fatalf("should not flag upgraded-to-safe version, got: %s", w)
		}
	}
}

func TestCheckDependencyVulns_GoJwtDeprecated(t *testing.T) {
	// dgrijalva/jwt-go is permanently deprecated
	oldGoMod := `module example.com/test`
	newGoMod := `module example.com/test

require github.com/dgrijalva/jwt-go v3.2.0
`
	warnings := checkDependencyVulns("go.mod", oldGoMod, newGoMod)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "dgrijalva/jwt-go") && strings.Contains(w, "DEPRECATED") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected deprecated jwt-go warning, got: %v", warnings)
	}
}
