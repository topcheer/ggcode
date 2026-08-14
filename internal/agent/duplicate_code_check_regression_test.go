package agent

import (
	"strings"
	"testing"
)

// Regression tests for issue #317: duplicate_code_check semantics bugs.

const dupMethodOld = `package main

type Server struct{ a, b int }

func (s *Server) handleGet(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}

func (s *Server) handlePost(values []string) []string {
	result := make([]string, 0)
	for _, item := range values {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`

func TestCheckDuplicateCode_DeltaAware_PreexistingDuplicateMethods(t *testing.T) {
	// Old content contains two similar METHODS plus everything else; new
	// content only adds an unrelated function. Pre-existing duplicate methods
	// must NOT re-trigger (delta filter must match "*Server.handleGet" etc.).
	new := dupMethodOld + `
func unrelated(x int) int {
	if x > 0 {
		return x * 2
	}
	return 0
}
`
	warnings := checkDuplicateCode("test.go", dupMethodOld, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for pre-existing duplicate methods after unrelated edit, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckDuplicateCode_ReversedOrder_NotExactClone(t *testing.T) {
	// Two functions with identical token frequency multisets but DIFFERENT
	// token sequence order (stmt order swapped) must NOT be "exact clone".
	// Note: the swapped statements must normalize differently, otherwise the
	// sequences are genuinely identical (Type-2 exact clone).
	src := `package main

func forward(x int) int {
	result := 0
	if x > 0 {
		result = x * 2
	}
	result = result + 1
	return result
}

func backward(x int) int {
	result := 0
	result = result + 1
	if x > 0 {
		result = x * 2
	}
	return result
}
`
	warnings := checkDuplicateCode("test.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "exact clone") {
			t.Fatalf("reversed-order functions must not be labeled exact clone: %s", w)
		}
	}
}

func TestCheckDuplicateCode_RealExactClone_StillExact(t *testing.T) {
	// True exact clones (identical token sequence) must still be labeled exact.
	src := `package main

func processStrings(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}

func transformValues(values []string) []string {
	result := make([]string, 0)
	for _, item := range values {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	old := `package main

func processStrings(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	warnings := checkDuplicateCode("test.go", old, src)
	if len(warnings) == 0 {
		t.Fatal("expected duplicate warning for exact clone")
	}
	if !strings.Contains(warnings[0], "exact clone") {
		t.Fatalf("expected exact clone label, got: %s", warnings[0])
	}
}

func TestExtractFuncNames_MethodReceiverPrefix(t *testing.T) {
	names := extractFuncNames(dupMethodOld)
	expected := []string{
		"handleGet", "handlePost", // bare names (backward compat)
		"*Server.handleGet", "*Server.handlePost", // receiver-prefixed, matching funcDeclName
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected name %q to be found", name)
		}
	}
}

func TestExtractFuncNames_InvalidGoFallback(t *testing.T) {
	// Old content that fails to parse should still yield bare names.
	src := "func broken( {\nfunc (s *Server) handle() {}\n"
	names := extractFuncNames(src)
	if !names["handle"] {
		t.Errorf("expected fallback extraction to find handle, got: %v", names)
	}
}
