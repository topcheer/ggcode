package agent

import (
	"strings"
	"testing"
)

func TestConfigSyntaxCheck_ValidJSON(t *testing.T) {
	valid := `{"name": "test", "version": "1.0", "nested": {"key": "value"}}`
	if warn := configSyntaxCheck("config.json", valid); warn != "" {
		t.Errorf("expected no warning for valid JSON, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_InvalidJSON(t *testing.T) {
	invalid := `{"name": "test", "version": "1.0", missing_quote: "value"}`
	warn := configSyntaxCheck("config.json", invalid)
	if warn == "" {
		t.Fatal("expected warning for invalid JSON, got none")
	}
	if !strings.Contains(warn, "JSON syntax error") {
		t.Errorf("expected JSON syntax error in warning, got: %s", warn)
	}
	if !strings.Contains(warn, "config.json") {
		t.Errorf("expected file path in warning, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_InvalidJSONTrailingComma(t *testing.T) {
	invalid := `{"name": "test", "items": [1, 2, 3,],}`
	warn := configSyntaxCheck("data.json", invalid)
	if warn == "" {
		t.Fatal("expected warning for JSON with trailing comma, got none")
	}
}

func TestConfigSyntaxCheck_EmptyFile(t *testing.T) {
	if warn := configSyntaxCheck("empty.json", ""); warn != "" {
		t.Errorf("expected no warning for empty file, got: %s", warn)
	}
	if warn := configSyntaxCheck("empty.json", "   \n  "); warn != "" {
		t.Errorf("expected no warning for whitespace-only file, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_UnsupportedExtension(t *testing.T) {
	if warn := configSyntaxCheck("readme.md", "# Title\n\nSome content"); warn != "" {
		t.Errorf("expected no warning for .md file, got: %s", warn)
	}
	if warn := configSyntaxCheck("script.py", "print('hello')"); warn != "" {
		t.Errorf("expected no warning for .py file, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_ValidYAML(t *testing.T) {
	valid := `
name: test
version: 1.0
nested:
  key: value
  list:
    - item1
    - item2
`
	if warn := configSyntaxCheck("config.yaml", valid); warn != "" {
		t.Errorf("expected no warning for valid YAML, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_InvalidYAML(t *testing.T) {
	// Tabs are not allowed in YAML
	invalid := "name: test\n\tsomething: bad"
	warn := configSyntaxCheck("config.yaml", invalid)
	if warn == "" {
		t.Fatal("expected warning for invalid YAML (tab), got none")
	}
	if !strings.Contains(warn, "YAML syntax error") {
		t.Errorf("expected YAML syntax error in warning, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_YAMLWithTabs(t *testing.T) {
	valid := `
server:
  port: 8080
  host: localhost
database:
  url: postgres://localhost/db
`
	if warn := configSyntaxCheck("docker-compose.yml", valid); warn != "" {
		t.Errorf("expected no warning for valid .yml file, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_ValidTOML(t *testing.T) {
	valid := `
title = "TOML Example"

[owner]
name = "Tom Preston-Werner"
dob = 1979-05-27T07:32:00Z

[database]
server = "192.168.1.1"
ports = [ 8001, 8001, 8002 ]
`
	if warn := configSyntaxCheck("Cargo.toml", valid); warn != "" {
		t.Errorf("expected no warning for valid TOML, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_InvalidTOML(t *testing.T) {
	// TOML doesn't allow this syntax
	invalid := `title = "bad"`
	invalid += "\n[owner\nname = " // broken section header + incomplete
	warn := configSyntaxCheck("Cargo.toml", invalid)
	if warn == "" {
		t.Fatal("expected warning for invalid TOML, got none")
	}
	if !strings.Contains(warn, "TOML syntax error") {
		t.Errorf("expected TOML syntax error in warning, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_ValidXML(t *testing.T) {
	valid := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <child attr="value">text</child>
  <empty/>
</root>`
	if warn := configSyntaxCheck("pom.xml", valid); warn != "" {
		t.Errorf("expected no warning for valid XML, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_InvalidXML(t *testing.T) {
	invalid := `<?xml version="1.0"?>
<root>
  <child>unclosed`
	warn := configSyntaxCheck("config.xml", invalid)
	if warn == "" {
		t.Fatal("expected warning for invalid XML, got none")
	}
	if !strings.Contains(warn, "XML syntax error") {
		t.Errorf("expected XML syntax error in warning, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_ValidSVG(t *testing.T) {
	valid := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
  <rect width="100" height="100" fill="red"/>
</svg>`
	if warn := configSyntaxCheck("icon.svg", valid); warn != "" {
		t.Errorf("expected no warning for valid SVG, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_JSONC(t *testing.T) {
	// JSON with comments
	valid := `{
  // This is a comment
  "name": "test",
  /* Block comment */
  "version": "1.0"
}`
	if warn := configSyntaxCheck("tsconfig.jsonc", valid); warn != "" {
		t.Errorf("expected no warning for valid JSONC, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_JSONCInvalid(t *testing.T) {
	invalid := `{
  // comment
  "name": "test",
  "broken": [1, 2,]
}`
	warn := configSyntaxCheck("tsconfig.jsonc", invalid)
	if warn == "" {
		t.Fatal("expected warning for invalid JSONC, got none")
	}
}

func TestConfigSyntaxCheck_LargeFile(t *testing.T) {
	// Build a file > maxConfigFileSize (512KB)
	large := `{"data": "`
	large += strings.Repeat("x", 600*1024)
	large += `"}` // valid JSON but oversized
	if warn := configSyntaxCheck("large.json", large); warn != "" {
		t.Errorf("expected no warning for oversized file (skipped), got: %s", warn)
	}
}

func TestStripJSONComments_LineComments(t *testing.T) {
	input := `{
	  // line comment
	  "a": 1,
	  "b": 2 // trailing comment
	}`
	result, err := stripJSONComments(input)
	if err != nil {
		t.Fatalf("stripJSONComments() unexpected error: %v", err)
	}
	if warn := validateJSON("test.json", result); warn != "" {
		t.Errorf("stripped JSONC should be valid, got: %s", warn)
	}
}

func TestStripJSONComments_BlockComments(t *testing.T) {
	input := `{
	  /* multi
	     line
	     comment */
	  "a": 1,
	  "b": /* inline */ 2
	}`
	result, err := stripJSONComments(input)
	if err != nil {
		t.Fatalf("stripJSONComments() unexpected error: %v", err)
	}
	if warn := validateJSON("test.json", result); warn != "" {
		t.Errorf("stripped JSONC should be valid, got: %s", warn)
	}
}

func TestStripJSONComments_StringWithCommentChars(t *testing.T) {
	// Comments inside strings should be preserved
	input := `{
	  "url": "http://example.com", // real comment
	  "regex": "//not a comment"
	}`
	result, err := stripJSONComments(input)
	if err != nil {
		t.Fatalf("stripJSONComments() unexpected error: %v", err)
	}
	// The string value should be preserved
	if !strings.Contains(result, "//not a comment") {
		t.Errorf("comment-like chars inside string should be preserved, got: %s", result)
	}
	if warn := validateJSON("test.json", result); warn != "" {
		t.Errorf("result should be valid JSON, got: %s", warn)
	}
}

func TestConfigSyntaxCheck_IntegrationValidYAML(t *testing.T) {
	// Valid YAML should not produce warnings
	warn := checkWriteIntegrity("deployment.yaml", "", "key: value\nlist:\n  - a\n  - b\n")
	if warn != "" {
		t.Errorf("expected no warning for valid YAML write, got: %s", warn)
	}
}
