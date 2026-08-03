package agent

import (
	"strings"
	"testing"
)

func TestCheckRangeCopyMod_FieldAssignment(t *testing.T) {
	src := `package main

type Item struct {
	Val int
}

func modify(items []Item) {
	for _, item := range items {
		item.Val = 42
	}
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "item") || !strings.Contains(warnings[0], "Val") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "index-based") {
		t.Errorf("warning should suggest index-based access: %s", warnings[0])
	}
}

func TestCheckRangeCopyMod_AddressOf(t *testing.T) {
	src := `package main

type Item struct {
	Val int
}

func process(items []Item) {
	for _, item := range items {
		modifyItem(&item)
	}
}
func modifyItem(i *Item) {}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "&slice[i]") {
		t.Errorf("warning should suggest &slice[i]: %s", warnings[0])
	}
}

func TestCheckRangeCopyMod_NoValueVar(t *testing.T) {
	// for i := range - no value variable, no issue
	src := `package main

func process(items []int) {
	for i := range items {
		items[i] = 0
	}
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for index-only range, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRangeCopyMod_BlankValue(t *testing.T) {
	// for _, _ := range - blank value
	src := `package main

func process(items []int) {
	for _, _ = range items {
	}
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for blank value, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRangeCopyMod_UnmodifiedValueVar(t *testing.T) {
	// Value var is read but not modified
	src := `package main

func process(items []int) {
	total := 0
	for _, v := range items {
		total += v
	}
	_ = total
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for read-only value, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRangeCopyMod_DeltaAware(t *testing.T) {
	old := `package main

type Item struct {
	Val int
}

func modify(items []Item) {
	for _, item := range items {
		item.Val = 42
	}
}
`
	// Same content - should produce no new warnings
	warnings := checkRangeCopyMod("test.go", old, old)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 delta warnings, got %d: %v", len(warnings), warnings)
	}

	// New pattern added
	newSrc := old + `
func modify2(more []Item) {
	for _, it := range more {
		it.Val = 99
	}
}
`
	warnings = checkRangeCopyMod("test.go", old, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 new delta warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "it") || !strings.Contains(warnings[0], "Val") {
		t.Errorf("unexpected delta warning: %s", warnings[0])
	}
}

func TestCheckRangeCopyMod_NonGoFile(t *testing.T) {
	warnings := checkRangeCopyMod("test.py", "", "for item in items:\n    item.val = 1")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckRangeCopyMod_TestFileSkipped(t *testing.T) {
	src := `package main

type Item struct {
	Val int
}

func modify(items []Item) {
	for _, item := range items {
		item.Val = 42
	}
}
`
	warnings := checkRangeCopyMod("main_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for test file, got %d", len(warnings))
	}
}

func TestCheckRangeCopyMod_MultipleFields(t *testing.T) {
	src := `package main

type Item struct {
	A int
	B int
}

func modify(items []Item) {
	for _, item := range items {
		item.A = 1
		item.B = 2
	}
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings for two field mods, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRangeCopyMod_SyntaxError(t *testing.T) {
	src := `package main

func broken( {
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unparseable file, got %d", len(warnings))
	}
}

func TestCheckRangeCopyMod_NestedRange(t *testing.T) {
	// Range variable modified inside a nested block
	src := `package main

type Item struct {
	Val int
}

func modify(items []Item) {
	for _, item := range items {
		if item.Val > 0 {
			item.Val = 0
		}
	}
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for nested modification, got %d: %v", len(warnings), warnings)
	}
}
