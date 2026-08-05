package agent

import (
	"strings"
	"testing"
)

// jt builds a Go struct tag literal for use in test source code.
// jt(`json:"name"`) produces the string: `json:"name"`
func jt(tag string) string {
	return "`" + tag + "`"
}

func TestStructTagConsistency_PascalCase(t *testing.T) {
	src := "package main\n\ntype Config struct {\n\tUserName string " + jt(`json:"UserName"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected PascalCase warning, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "PascalCase") || strings.Contains(w, "uppercase") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PascalCase warning, got: %v", warnings)
	}
}

func TestStructTagConsistency_RedundantTag(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tName string " + jt(`json:"Name"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "redundant") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected redundant tag warning, got: %v", warnings)
	}
}

func TestStructTagConsistency_CorrectTags(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tName  string " + jt(`json:"name"`) + "\n\tEmail string " + jt(`json:"email"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for correct tags, got: %v", warnings)
	}
}

func TestStructTagConsistency_InconsistentCoverage(t *testing.T) {
	src := "package main\n\ntype Response struct {\n\tUserName string " + jt(`json:"user_name"`) + "\n\tEmail    string " + jt(`json:"email"`) + "\n\tTitle    string\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Title") && strings.Contains(w, "no json tag") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected inconsistent coverage warning for Title, got: %v", warnings)
	}
}

func TestStructTagConsistency_OmittedField(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tName     string " + jt(`json:"name"`) + "\n\tEmail    string " + jt(`json:"email"`) + "\n\tInternal string " + jt(`json:"-"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "Internal") {
			t.Fatalf("json:\"-\" field should not be flagged, but got: %s", w)
		}
	}
}

func TestStructTagConsistency_NoJSONModel(t *testing.T) {
	src := "package main\n\ntype Internal struct {\n\tData string\n\tCache int\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for struct without json tags, got: %v", warnings)
	}
}

func TestStructTagConsistency_SingleTaggedField(t *testing.T) {
	src := "package main\n\ntype Config struct {\n\tPort int    " + jt(`json:"port"`) + "\n\tHost string\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "Host") && strings.Contains(w, "no json tag") {
			t.Fatalf("single tagged field should not trigger coverage warning, got: %s", w)
		}
	}
}

func TestStructTagConsistency_EmbeddedFields(t *testing.T) {
	src := "package main\n\ntype Base struct {\n\tID int " + jt(`json:"id"`) + "\n}\n\ntype Extended struct {\n\tBase\n\tName string " + jt(`json:"name"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "no json tag") && strings.Contains(w, "Base") {
			t.Fatalf("embedded field should not be flagged, got: %s", w)
		}
	}
}

func TestStructTagConsistency_UnexportedFields(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tName  string " + jt(`json:"name"`) + "\n\temail string " + jt(`json:"email"`) + "\n\tcache int\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "cache") && strings.Contains(w, "no json tag") {
			t.Fatalf("unexported field should not be flagged for coverage, got: %s", w)
		}
	}
}

func TestStructTagConsistency_OptionsOnly(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tName string " + jt(`json:",omitempty"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "PascalCase") || strings.Contains(w, "uppercase") {
			t.Fatalf("options-only tag should not trigger PascalCase warning, got: %s", w)
		}
	}
}

func TestStructTagConsistency_TestFileExcluded(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tName string " + jt(`json:"Name"`) + "\n}"
	warnings := checkStructTagConsistency("main_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("test files should be excluded, got: %v", warnings)
	}
}

func TestStructTagConsistency_NonGoFile(t *testing.T) {
	warnings := checkStructTagConsistency("main.py", "", "type User struct{}")
	if len(warnings) != 0 {
		t.Fatalf("non-Go files should be excluded, got: %v", warnings)
	}
}

func TestStructTagConsistency_EmptyContent(t *testing.T) {
	warnings := checkStructTagConsistency("main.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("empty content should produce no warnings, got: %v", warnings)
	}
}

func TestStructTagConsistency_SyntaxError(t *testing.T) {
	src := "package main\ntype User struct {"
	warnings := checkStructTagConsistency("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("syntax errors should produce no warnings, got: %v", warnings)
	}
}

func TestStructTagConsistency_CamelCaseOK(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tFirstName string " + jt(`json:"firstName"`) + "\n\tLastName  string " + jt(`json:"lastName"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("camelCase tags should produce no warnings, got: %v", warnings)
	}
}

func TestStructTagConsistency_SnakeCaseOK(t *testing.T) {
	src := "package main\n\ntype User struct {\n\tFirstName string " + jt(`json:"first_name"`) + "\n\tLastName  string " + jt(`json:"last_name"`) + "\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("snake_case tags should produce no warnings, got: %v", warnings)
	}
}

func TestStructTagConsistency_WarningCap(t *testing.T) {
	// Create a struct with many untagged exported fields to trigger the cap
	var lines []string
	lines = append(lines, "package main", "", "type Big struct {")
	lines = append(lines, "\tA string "+jt(`json:"a"`))
	lines = append(lines, "\tB string "+jt(`json:"b"`))
	for i := 0; i < 20; i++ {
		lines = append(lines, "\tF"+itoaSimple(i)+" string")
	}
	lines = append(lines, "}")
	src := strings.Join(lines, "\n")
	warnings := checkStructTagConsistency("main.go", "", src)
	if len(warnings) < maxStructTagWarnings {
		t.Fatalf("expected at least %d warnings for many untagged fields, got %d", maxStructTagWarnings, len(warnings))
	}
	last := warnings[len(warnings)-1]
	if !strings.Contains(last, "capped") {
		t.Fatalf("expected cap notice in last warning, got: %s", last)
	}
}

func TestStructTagConsistency_QuotedTagLiteral(t *testing.T) {
	// Struct tags can be quote-delimited (rare but valid)
	src := "package main\n\ntype User struct {\n\tName string \"json:\\\"Name\\\"\"\n}"
	warnings := checkStructTagConsistency("main.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "redundant") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected redundant warning for quote-delimited tag, got: %v", warnings)
	}
}

func TestTagBaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"name", "name"},
		{"name,omitempty", "name"},
		{",omitempty", ""},
		{"-", "-"},
		{"-,", "-"},
		{"", ""},
	}
	for _, tt := range tests {
		got := tagBaseName(tt.input)
		if got != tt.want {
			t.Errorf("tagBaseName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLowerFirst(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Name", "name"},
		{"JSON", "jSON"},
		{"abc", "abc"},
		{"", ""},
	}
	for _, tt := range tests {
		got := lowerFirst(tt.input)
		if got != tt.want {
			t.Errorf("lowerFirst(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
