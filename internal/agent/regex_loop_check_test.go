package agent

import (
	"strings"
	"testing"
)

func TestCheckRegexLoop_DetectsMustCompileInForLoop(t *testing.T) {
	newCode := `package main

import "regexp"

func validate(inputs []string) []bool {
	results := make([]bool, len(inputs))
	for i := 0; i < len(inputs); i++ {
		re := regexp.MustCompile("^[0-9]+$")
		results[i] = re.MatchString(inputs[i])
	}
	return results
}
`
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for regexp.MustCompile inside for loop")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "regexp.MustCompile") && strings.Contains(w, "inside loop") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about regexp.MustCompile inside loop, got: %v", warnings)
	}
}

func TestCheckRegexLoop_DetectsCompileInRangeLoop(t *testing.T) {
	newCode := `package main

import "regexp"

func validate(inputs []string) bool {
	for _, input := range inputs {
		re, err := regexp.Compile(input)
		if err != nil {
			continue
		}
		if re.MatchString("test") {
			return true
		}
	}
	return false
}
`
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for regexp.Compile inside range loop")
	}
}

func TestCheckRegexLoop_DetectsCompilePOSIX(t *testing.T) {
	newCode := `package main

import "regexp"

func validate(inputs []string) {
	for _, input := range inputs {
		re := regexp.MustCompilePOSIX(input)
		_ = re
	}
}
`
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for regexp.MustCompilePOSIX inside range loop")
	}
}

func TestCheckRegexLoop_NoWarningForPackageLevelCompile(t *testing.T) {
	newCode := `package main

import "regexp"

var validPattern = regexp.MustCompile("^[0-9]+$")

func validate(inputs []string) []bool {
	results := make([]bool, len(inputs))
	for i, input := range inputs {
		results[i] = validPattern.MatchString(input)
	}
	return results
}
`
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for package-level compile, got: %v", warnings)
	}
}

func TestCheckRegexLoop_NoWarningWhenNotInLoop(t *testing.T) {
	newCode := `package main

import "regexp"

func validate(input string) bool {
	re := regexp.MustCompile("^[0-9]+$")
	return re.MatchString(input)
}
`
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when regex compile is outside loop, got: %v", warnings)
	}
}

func TestCheckRegexLoop_DeltaAwareNoWarningIfAlreadyExisted(t *testing.T) {
	oldCode := `package main

import "regexp"

func validate(inputs []string) {
	for _, input := range inputs {
		re := regexp.MustCompile(input)
		_ = re
	}
}
`
	// newCode adds an unrelated line but keeps the same regex-in-loop
	newCode := oldCode + "// done\n"
	warnings := checkRegexLoop("test.go", oldCode, newCode)
	if len(warnings) != 0 {
		t.Fatalf("expected no delta warnings when pattern already existed, got: %v", warnings)
	}
}

func TestCheckRegexLoop_DeltaAwareFlagsNewlyIntroduced(t *testing.T) {
	oldCode := `package main

import "regexp"

func validate(inputs []string) {
	for _, input := range inputs {
		_ = input
	}
}
`
	newCode := `package main

import "regexp"

func validate(inputs []string) {
	for _, input := range inputs {
		re := regexp.MustCompile(input)
		_ = re
	}
}
`
	warnings := checkRegexLoop("test.go", oldCode, newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warning for newly introduced regex-in-loop")
	}
}

func TestCheckRegexLoop_SkipsNonGoFiles(t *testing.T) {
	newCode := `const x = regexp.MustCompile("test");`
	warnings := checkRegexLoop("test.js", "", newCode)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckRegexLoop_SkipsNestedFuncLit(t *testing.T) {
	newCode := `package main

import "regexp"

func validate(inputs []string) {
	for _, input := range inputs {
		func() {
			re := regexp.MustCompile(input)
			_ = re
		}()
	}
}
`
	// regex compile inside a FuncLit nested in a loop should NOT be flagged
	// because it's a separate scope (may be called via goroutine etc.)
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for regex compile in nested func literal, got: %v", warnings)
	}
}

func TestCheckRegexLoop_DetectsInNestedIfInsideLoop(t *testing.T) {
	newCode := `package main

import "regexp"

func validate(inputs []string) {
	for _, input := range inputs {
		if len(input) > 0 {
			re := regexp.MustCompile(input)
			_ = re
		}
	}
}
`
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warning for regex compile inside nested if within loop")
	}
}

func TestCheckRegexLoop_EmptyContent(t *testing.T) {
	warnings := checkRegexLoop("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCheckRegexLoop_MultipleCompilesCapped(t *testing.T) {
	newCode := `package main

import "regexp"

func validate(inputs []string) {
	for _, input := range inputs {
		re1 := regexp.MustCompile(input)
		re2 := regexp.CompilePOSIX(input)
		re3 := regexp.Compile(input)
		_ = re1
		_ = re2
		_ = re3
	}
}
`
	warnings := checkRegexLoop("test.go", "", newCode)
	if len(warnings) < 3 {
		t.Fatalf("expected at least 3 warnings (2 individual + 1 'more' summary), got %d", len(warnings))
	}
}
