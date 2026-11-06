package tool

import (
	"strings"
	"testing"
)

// Issue #5130 face 3: a multi-line replacement that ADDS a struct field
// keeps the declaration line intact, yet the #4796 warning misfired because
// the declaration regex match is a truncated prefix ("type X struct",
// stopping at \b before the brace) compared against FULL new-text lines.
// Repro from the reporter: duplicate_check.go field-add during #5121.
func TestIssue5130FieldAddNoSwallowWarning(t *testing.T) {
	oldText := "type DuplicateCheck struct {\n\tSimilarTo       string\n\tSimilarity      float64\n\tExistingContent string\n}"
	newText := "type DuplicateCheck struct {\n\tSimilarTo       string\n\tSimilarity      float64\n\tExistingContent string\n\n\t// SameKey marks an exact same-key hit.\n\tSameKey bool\n}"
	if w := swallowedDeclarationWarning(oldText, newText); w != "" {
		t.Fatalf("field-add with declaration line preserved must NOT warn, got: %s", w)
	}
}

// A genuinely swallowed declaration (renamed/replaced) must still warn.
func TestIssue5130TrueSwallowStillWarns(t *testing.T) {
	oldText := "type GoneStruct struct {\n\tA string\n}"
	newText := "type Replacement struct {\n\tA string\n}"
	w := swallowedDeclarationWarning(oldText, newText)
	if w == "" {
		t.Fatal("a deleted declaration must still warn")
	}
	if !strings.Contains(w, "type GoneStruct struct") {
		t.Errorf("warning should name the vanished declaration, got: %s", w)
	}
}

// Boundary guard: a kept line whose text merely STARTS with the truncated
// match (different identifier) must NOT count as preserved.
func TestIssue5130PrefixBoundaryNotKept(t *testing.T) {
	oldText := "type Foo struct {\n\tA string\n}"
	newText := "type Foo structExtra {\n\tA string\n}"
	if w := swallowedDeclarationWarning(oldText, newText); w == "" {
		t.Fatal("type Foo struct{ swallowed while Foo structExtra{ exists must still warn (word-boundary prefix match)")
	}
}
