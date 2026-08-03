package agent

import (
	"strings"
	"testing"
)

func TestCheckLoopPerf_StringConcatInForLoop(t *testing.T) {
	old := `package main

func process(items []string) {
	s := ""
	for _, item := range items {
		_ = item
	}
	_ = s
}
`
	new := `package main

func process(items []string) {
	s := ""
	for _, item := range items {
		s += item + ", "
	}
	_ = s
}
`
	warnings := checkLoopPerf("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for string += in for loop")
	}
	if !strings.Contains(warnings[0], "O(n^2)") {
		t.Errorf("warning should mention O(n^2), got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "string concat") {
		t.Errorf("warning should mention string concat, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "strings.Builder") {
		t.Errorf("warning should suggest strings.Builder, got: %s", warnings[0])
	}
}

func TestCheckLoopPerf_StringConcatInRangeLoop(t *testing.T) {
	old := `package main

func build(rows []Row) string {
	result := ""
	for _, r := range rows {
		_ = r
	}
	return result
}
`
	new := `package main

func build(rows []Row) string {
	result := ""
	for _, r := range rows {
		result += r.Text
	}
	return result
}
`
	warnings := checkLoopPerf("build.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for string += in range loop")
	}
}

func TestCheckLoopPerf_FmtSprintfInLoop(t *testing.T) {
	old := `package main

import "fmt"

func format(records []Record) string {
	buf := ""
	for _, r := range records {
		_ = r
	}
	return buf
}
`
	new := `package main

import "fmt"

func format(records []Record) string {
	buf := ""
	for _, r := range records {
		buf += fmt.Sprintf("%d:%s ", r.ID, r.Name)
	}
	return buf
}
`
	warnings := checkLoopPerf("format.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for fmt.Sprintf in loop")
	}
	if !strings.Contains(warnings[0], "fmt.Sprintf") {
		t.Errorf("warning should mention fmt.Sprintf, got: %s", warnings[0])
	}
}

func TestCheckLoopPerf_StringsBuilderAllowed(t *testing.T) {
	old := `package main

import "strings"

func build(items []string) string {
	var b strings.Builder
	for _, item := range items {
		_ = item
	}
	return b.String()
}
`
	new := `package main

import "strings"

func build(items []string) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item)
	}
	return b.String()
}
`
	warnings := checkLoopPerf("build.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for strings.Builder, got: %v", warnings)
	}
}

func TestCheckLoopPerf_NonStringPlusAssignAllowed(t *testing.T) {
	old := `package main

func sum(nums []int) int {
	total := 0
	for _, n := range nums {
		_ = n
	}
	return total
}
`
	new := `package main

func sum(nums []int) int {
	total := 0
	for _, n := range nums {
		total += n
	}
	return total
}
`
	warnings := checkLoopPerf("sum.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for int += in loop, got: %v", warnings)
	}
}

func TestCheckLoopPerf_DeltaAware(t *testing.T) {
	// Old content already has the anti-pattern.
	old := `package main

func process(items []string) {
	s := ""
	for _, item := range items {
		s += item + ", "
	}
	_ = s
}
`
	// New content has the same pattern - should NOT warn.
	new := old

	warnings := checkLoopPerf("process.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for pre-existing pattern, got: %v", warnings)
	}
}

func TestCheckLoopPerf_NewFileDetected(t *testing.T) {
	new := `package main

func concat(lines []string) string {
	result := ""
	for _, line := range lines {
		result += line + "\n"
	}
	return result
}
`
	warnings := checkLoopPerf("concat.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for new file with string += in loop")
	}
}

func TestCheckLoopPerf_NestedLoop(t *testing.T) {
	old := `package main

func process(grid [][]string) string {
	out := ""
	for _, row := range grid {
		for _, cell := range row {
			_ = cell
		}
	}
	return out
}
`
	new := `package main

func process(grid [][]string) string {
	out := ""
	for _, row := range grid {
		for _, cell := range row {
			out += cell
		}
	}
	return out
}
`
	warnings := checkLoopPerf("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for string += in nested loop")
	}
}

func TestCheckLoopPerf_SkipTestFiles(t *testing.T) {
	new := `package main

func process(items []string) {
	s := ""
	for _, item := range items {
		s += item + ", "
	}
	_ = s
}
`
	warnings := checkLoopPerf("process_test.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for test file, got: %v", warnings)
	}
}

func TestCheckLoopPerf_NonGoFile(t *testing.T) {
	new := `func process(items) {
	let s = "";
	for (item of items) {
		s += item + ", ";
	}
}
`
	warnings := checkLoopPerf("process.js", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for .js file, got: %v", warnings)
	}
}

func TestCheckLoopPerf_VarDeclaredWithStringTypeAnnotation(t *testing.T) {
	old := `package main

func process(items []string) {
	var s string
	for _, item := range items {
		_ = item
	}
	_ = s
}
`
	new := `package main

func process(items []string) {
	var s string
	for _, item := range items {
		s += item
	}
	_ = s
}
`
	warnings := checkLoopPerf("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for string += with type annotation")
	}
}

func TestCheckLoopPerf_AssignedStringVar(t *testing.T) {
	// Variable assigned from a string-returning function should be tracked.
	old := `package main

import "strings"

func process(items []string) {
	s := strings.Join(items, ",")
	for _, item := range items {
		_ = item
	}
	_ = s
}
`
	new := `package main

import "strings"

func process(items []string) {
	s := strings.Join(items, ",")
	for _, item := range items {
		s += item
	}
	_ = s
}
`
	warnings := checkLoopPerf("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for string += where var initialized from strings.Join")
	}
}
