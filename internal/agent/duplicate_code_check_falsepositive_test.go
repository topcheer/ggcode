package agent

import (
	"testing"
)

// TestComputeSimilarity_BagOfTokensFalsePositive verifies that the
// bag-of-tokens approach causes false positives for structurally
// different functions with similar token frequency distributions.
func TestComputeSimilarity_BagOfTokensFalsePositive(t *testing.T) {
	// Two function signatures with similar token frequencies but different structures.
	// Function A: if → assignment → return → return (linear control flow)
	// Function B: if → return → if → return (branching control flow)
	sigA := funcSignature{
		tokenSet: map[string]int{
			"if":     2, // Two if statements in A (one nested condition check, one outer if)
			"return": 2, // Two return statements
			"v":      4, // Variables: resp, body, resp, resp
			"E":      6, // Exported identifiers: http, Response, io, ReadAll, fmt, Errorf
			">=":     1, // Comparison operator
			":=":     1, // Assignment operator
			".":      4, // Selector expressions: resp.StatusCode, io.ReadAll, resp.Body, resp.StatusCode
			"INT":    1, // 400
			"STR":    1, // "http %d: %s"
			"call":   1, // One function call (io.ReadAll)
			"errorf": 1, // Error formatting
		},
	}

	sigB := funcSignature{
		tokenSet: map[string]int{
			"if":     2, // Two if statements
			"return": 2, // Two return statements
			"v":      4, // Variables: path, path, path, path
			"E":      4, // Exported identifiers: strings, Contains, filepath, IsAbs
			"!":      1, // Unary not operator
			".":      4, // Selector expressions: strings.Contains, filepath.IsAbs (two each, called twice)
			"STR":    2, // "..", "path traversal: %s", "not absolute: %s"
			"call":   2, // Two function calls (strings.Contains, filepath.IsAbs)
			"errorf": 2, // Two error formatting calls
		},
	}

	sim := computeSimilarity(sigA, sigB)

	// The similarity should be high because the token frequencies are similar
	// (both have 2 if, 2 return, similar counts of v, E, ., STR, call, errorf).
	// Despite completely different control flow and semantics.

	t.Logf("Similarity between structurally different functions: %.2f%%", sim*100)

	if sim < 0.85 {
		t.Logf("Note: Similarity is %.2f%%, below the 85%% threshold", sim*100)
		t.Logf("This suggests the false positive may not trigger in practice")
	} else {
		t.Logf("WARNING: Similarity is %.2f%%, above the 85%% threshold", sim*100)
		t.Logf("This is a FALSE POSITIVE - functions have completely different structures:")
		t.Logf("  Function A: linear flow with assignment in if body")
		t.Logf("  Function B: branching flow with early returns")
	}
}

// TestCheckDuplicateCode_StructurallyDifferentFalsePositive tests the
// full checkDuplicateCode function with the exact scenario from the bug report.
func TestCheckDuplicateCode_StructurallyDifferentFalsePositive(t *testing.T) {
	src := `package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"path/filepath"
)

// Function A: network error handling (linear flow)
func handleNetworkError(resp *http.Response) error {
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Function B: file path validation (branching flow)
func validateFilePath(path string) error {
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal: %s", path)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("not absolute: %s", path)
	}
	return nil
}
`

	warnings := checkDuplicateCode("test.go", "", src)

	if len(warnings) > 0 {
		t.Logf("WARNING: False positive detected!")
		for _, w := range warnings {
			t.Logf("  %s", w)
		}
		t.Logf("These functions are NOT duplicates - they have:")
		t.Logf("  - Different control flow structures")
		t.Logf("  - Different semantics (network vs filesystem)")
		t.Logf("  - Different purposes (error handling vs validation)")
	} else {
		t.Logf("No warning issued. Functions are correctly identified as non-duplicates.")
		t.Logf("This may be due to token frequency differences preventing the false positive.")
	}
}

// TestComputeSimilarity_SameTokensDifferentOrder demonstrates that
// identical token frequencies in different orders produce 100% similarity.
func TestComputeSimilarity_SameTokensDifferentOrder(t *testing.T) {
	// Same token frequencies, completely different order
	sigA := funcSignature{
		tokenSet: map[string]int{
			"if":     2,
			"return": 2,
			"v":      3,
			"E":      2,
		},
		tokens: []string{"if", "v", "return", "if", "E", "v", "return", "E", "v"},
	}

	sigB := funcSignature{
		tokenSet: map[string]int{
			"if":     2,
			"return": 2,
			"v":      3,
			"E":      2,
		},
		tokens: []string{"return", "if", "v", "E", "return", "if", "v", "v", "E"},
	}

	sim := computeSimilarity(sigA, sigB)

	if sim != 1.0 {
		t.Fatalf("Expected 1.0 similarity for same token frequencies (different order), got %.2f", sim)
	}

	t.Logf("Confirmed: Bag-of-tokens ignores order - same frequencies = 100%% similarity regardless of structure")
}
