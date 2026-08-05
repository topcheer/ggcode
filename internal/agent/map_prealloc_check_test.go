package agent

import (
	"testing"
)

func TestMapPrealloc_RangeLoop(t *testing.T) {
	code := `package main
type Item struct { Key string; Val int }
func process(items []Item) map[string]int {
	m := make(map[string]int)
	for _, item := range items {
		m[item.Key] = item.Val
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) == 0 {
		t.Fatal("expected map preallocation warning for range loop")
	}
	if !strContains(warnings[0], "Map preallocation") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
	if !strContains(warnings[0], "len(items)") {
		t.Errorf("warning should suggest len(items) hint, got: %s", warnings[0])
	}
}

func TestMapPrealloc_ForLoop(t *testing.T) {
	code := `package main
func process(n int) map[int]bool {
	m := make(map[int]bool)
	for i := 0; i < n; i++ {
		m[i] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) == 0 {
		t.Fatal("expected map preallocation warning for for loop")
	}
}

func TestMapPrealloc_WithHint(t *testing.T) {
	code := `package main
type Item struct { Key string; Val int }
func process(items []Item) map[string]int {
	m := make(map[string]int, len(items))
	for _, item := range items {
		m[item.Key] = item.Val
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) > 0 {
		t.Fatalf("expected no warning for preallocated map, got: %v", warnings)
	}
}

func TestMapPrealloc_DeltaAware(t *testing.T) {
	oldCode := `package main
func process(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, s := range items {
		m[s] = true
	}
	return m
}`
	newCode := `package main
func process(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, s := range items {
		m[s] = true
	}
	other := make(map[string]bool)
	for _, s := range items {
		other[s] = true
	}
	return other
}`
	warnings := checkMapPrealloc("test.go", oldCode, newCode)
	// Should detect only the new "other" map, not the existing "m".
	if len(warnings) == 0 {
		t.Fatal("expected delta warning for new map")
	}
	// Should warn about "other" specifically
	foundOther := false
	for _, w := range warnings {
		if strContains(w, "other") {
			foundOther = true
		}
		if strContains(w, `"m"`) {
			t.Fatal("should not warn about pre-existing map m in delta")
		}
	}
	if !foundOther {
		t.Fatal("should warn about new map 'other'")
	}
}

func TestMapPrealloc_ConditionalSkip(t *testing.T) {
	code := `package main
type Item struct { Key string; Val int; Active bool }
func process(items []Item) map[string]int {
	m := make(map[string]int)
	for _, item := range items {
		if item.Active {
			m[item.Key] = item.Val
		}
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	// Conditional skip means we can't reliably know the size.
	if len(warnings) > 0 {
		t.Fatalf("expected no warning for conditional map write, got: %v", warnings)
	}
}

func TestMapPrealloc_ShortDecl(t *testing.T) {
	code := `package main
func process(keys []string) map[string]int {
	m := make(map[string]int)
	for i, k := range keys {
		m[k] = i
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) == 0 {
		t.Fatal("expected warning for short-decl map in loop")
	}
}

func TestMapPrealloc_NotGoFile(t *testing.T) {
	code := `// not go`
	warnings := checkMapPrealloc("test.py", "", code)
	if len(warnings) > 0 {
		t.Fatal("expected no warning for non-Go file")
	}
}

func TestMapPrealloc_TestFile(t *testing.T) {
	code := `package main
func process(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, s := range items {
		m[s] = true
	}
	return m
}`
	warnings := checkMapPrealloc("main_test.go", "", code)
	if len(warnings) > 0 {
		t.Fatal("expected no warning for test file")
	}
}

func TestMapPrealloc_MultipleMaps(t *testing.T) {
	code := `package main
func process(items []string) (map[string]bool, map[string]int) {
	a := make(map[string]bool)
	b := make(map[string]int)
	for _, s := range items {
		a[s] = true
		b[s] = len(s)
	}
	return a, b
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) < 2 {
		t.Fatalf("expected 2+ warnings for two unpreallocated maps, got %d: %v", len(warnings), warnings)
	}
}

func TestMapPrealloc_VarDecl(t *testing.T) {
	code := `package main
type Item struct { Key string; Val int }
func process(items []Item) map[string]int {
	var m = make(map[string]int)
	for _, item := range items {
		m[item.Key] = item.Val
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) == 0 {
		t.Fatal("expected warning for var-declared map")
	}
}

func TestMapPrealloc_SelectorSource(t *testing.T) {
	code := `package main
type Container struct { Items []string }
func process(c Container) map[string]bool {
	m := make(map[string]bool)
	for _, s := range c.Items {
		m[s] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) == 0 {
		t.Fatal("expected warning for map populated from selector expression")
	}
	if !strContains(warnings[0], "c.Items") {
		t.Errorf("warning should suggest len(c.Items), got: %s", warnings[0])
	}
}

func strContains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
