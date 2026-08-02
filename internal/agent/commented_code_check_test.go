package agent

import (
	"strings"
	"testing"
)

func TestCheckCommentedCodeBlocks_GoLines(t *testing.T) {
	// No old content — all new
	old := `package main

func foo() int {
	return 42
}
`
	newContent := `package main

func foo() int {
	// oldX := computeValue()
	// if oldX > 10 {
	//     return oldX
	// }
	return 42
}
`
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) == 0 {
		t.Error("expected warnings for commented-out Go code block")
	}
	if !strings.Contains(warnings[0], "Commented-out code block") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckCommentedCodeBlocks_NoNewBlock(t *testing.T) {
	// Old already has the commented block — should not warn
	old := `package main

func foo() int {
	// x := 1
	// y := 2
	// z := x + y
	return 42
}
`
	newContent := old // identical
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for pre-existing block, got: %v", warnings)
	}
}

func TestCheckCommentedCodeBlocks_PythonHash(t *testing.T) {
	old := `def foo():
    return 42
`
	newContent := `def foo():
    # old_val = compute()
    # if old_val > 10:
    #     return old_val
    return 42
`
	warnings := checkCommentedCodeBlocks("main.py", old, newContent)
	if len(warnings) == 0 {
		t.Error("expected warnings for commented-out Python code block")
	}
}

func TestCheckCommentedCodeBlocks_DocumentationNotFlagged(t *testing.T) {
	// Prose comments should not be flagged
	old := `package main`
	newContent := `package main

// This function computes the value by doing complex calculations
// and returns the result as an integer. It handles edge cases
// like negative inputs and overflow gracefully.
func foo() int {
	return 42
}
`
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for prose documentation, got: %v", warnings)
	}
}

func TestCheckCommentedCodeBlocks_TooShortNotFlagged(t *testing.T) {
	// Only 1-2 lines of commented code should not trigger
	old := `package main`
	newContent := `package main

func foo() int {
	// x := 1
	return 42
}
`
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for short comment, got: %v", warnings)
	}
}

func TestCheckCommentedCodeBlocks_UnsupportedExt(t *testing.T) {
	warnings := checkCommentedCodeBlocks("file.unknownext", "", "some content")
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for unsupported extension, got: %v", warnings)
	}
}

func TestCheckCommentedCodeBlocks_JavaScript(t *testing.T) {
	old := `function foo() {
	return 42;
}
`
	newContent := `function foo() {
	// const result = await fetch(url);
	// const data = await result.json();
	// console.log(data.items);
	return 42;
}
`
	warnings := checkCommentedCodeBlocks("foo.js", old, newContent)
	if len(warnings) == 0 {
		t.Error("expected warnings for commented-out JS code block")
	}
}

func TestCheckCommentedCodeBlocks_EmptyLinesInBlock(t *testing.T) {
	// Empty comment lines within a block should not break detection
	old := `package main`
	newContent := `package main

func foo() int {
	// x := getValue()
	//
	// if x > threshold {
	//     return x
	// }
	return 0
}
`
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) == 0 {
		t.Error("expected warnings for commented-out block with empty comment lines")
	}
}

func TestLooksLikeCode(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"x = 5", true},          // assignment
		{"foo(bar)", true},       // function call
		{"return x + y", true},   // return keyword
		{"if x > 10 {", true},    // if statement
		{"fmt.Println(x)", true}, // function call
		{"statement;", true},     // semicolon termination
		{"This is prose", false}, // no code indicators
		{"A documentation comment", false},
		{"", false},
	}

	for _, tt := range tests {
		got := looksLikeCode(tt.input)
		if got != tt.expected {
			t.Errorf("looksLikeCode(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestCheckCommentedCodeBlocks_LicenseHeaderNotFlagged(t *testing.T) {
	// License headers should not be flagged even if multi-line
	newContent := `// Copyright 2024 Acme Inc.
// Licensed under the Apache License, Version 2.0.
// You may not use this file except in compliance with the License.

package main
`
	warnings := checkCommentedCodeBlocks("main.go", "", newContent)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for license header, got: %v", warnings)
	}
}
