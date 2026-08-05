package agent

import (
	"strings"
	"testing"
)

func TestCheckMapIterWrite_DetectsDeleteDuringRange(t *testing.T) {
	old := `package main
func process(m map[string]int) {
	for k := range m {
		_ = k
	}
}`
	new := `package main
func process(m map[string]int) {
	for k := range m {
		delete(m, k)
	}
}`
	warnings := checkMapIterWrite("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected map-iter-write warning for delete during range")
	}
	if !strings.Contains(warnings[0], "map write/delete") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckMapIterWrite_DetectsAssignDuringRange(t *testing.T) {
	old := `package main
func process(m map[string]int) {
	for k := range m {
		_ = k
	}
}`
	new := `package main
func process(m map[string]int) {
	for k := range m {
		m[k] = 42
	}
}`
	warnings := checkMapIterWrite("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected map-iter-write warning for assign during range")
	}
}

func TestCheckMapIterWrite_DetectsNewKeyAssignDuringRange(t *testing.T) {
	old := `package main
func process(m map[string]int) {
	for k, v := range m {
		_ = k
		_ = v
	}
}`
	new := `package main
func process(m map[string]int) {
	for k, v := range m {
		m["new_"+k] = v + 1
	}
}`
	warnings := checkMapIterWrite("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for adding new key during range")
	}
}

func TestCheckMapIterWrite_NoWarningForOtherMapWrite(t *testing.T) {
	// Writing to a different map than the one being iterated is safe.
	old := `package main
func process(m map[string]int) {
	for k := range m {
		_ = k
	}
}`
	new := `package main
func process(m, other map[string]int) {
	for k := range m {
		other[k] = 1
	}
}`
	warnings := checkMapIterWrite("process.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for writing to different map, got: %v", warnings)
	}
}

func TestCheckMapIterWrite_NoWarningForNonMapRange(t *testing.T) {
	// Range over slice/array - no map iteration hazard.
	old := `package main
func process(items []int) {
	for i := range items {
		_ = i
	}
}`
	new := `package main
func process(items []int) {
	for i := range items {
		items[i] = 0
	}
}`
	warnings := checkMapIterWrite("process.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for slice range, got: %v", warnings)
	}
}

func TestCheckMapIterWrite_NoWarningWhenPreExisting(t *testing.T) {
	// Both old and new have same delete-in-range - no delta.
	src := `package main
func process(m map[string]int) {
	for k := range m {
		delete(m, k)
	}
}`
	warnings := checkMapIterWrite("process.go", src, src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when pattern is pre-existing, got: %v", warnings)
	}
}

func TestCheckMapIterWrite_DetectsNestedWriteInLoop(t *testing.T) {
	old := `package main
func process(m map[string]bool) {
	for k := range m {
		_ = k
	}
}`
	new := `package main
func process(m map[string]bool) {
	for k := range m {
		if m[k] {
			delete(m, k)
		}
	}
}`
	warnings := checkMapIterWrite("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for nested delete within range loop")
	}
}

func TestCheckMapIterWrite_DetectsStructFieldMap(t *testing.T) {
	old := `package main
type S struct{ items map[string]int }
func (s *S) process() {
	for k := range s.items {
		_ = k
	}
}`
	new := `package main
type S struct{ items map[string]int }
func (s *S) process() {
	for k := range s.items {
		s.items[k] = 0
	}
}`
	warnings := checkMapIterWrite("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for struct field map write during range")
	}
}

func TestCheckMapIterWrite_NoWarningForEmptyContent(t *testing.T) {
	warnings := checkMapIterWrite("process.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCheckMapIterWrite_NoWarningForNonGoFile(t *testing.T) {
	warnings := checkMapIterWrite("process.py", "", "for k in m: del m[k]")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckMapIterWrite_NoWarningForInvalidGo(t *testing.T) {
	warnings := checkMapIterWrite("process.go", "", "this is not valid go {{{")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for invalid Go, got: %v", warnings)
	}
}
