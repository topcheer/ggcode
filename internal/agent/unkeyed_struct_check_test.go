package agent

import (
	"strings"
	"testing"
)

func TestUnkeyedStruct_BasicDetection(t *testing.T) {
	src := `package main

type Config struct {
	Host string
	Port int
	TLS  bool
}

func main() {
	c := Config{"localhost", 8080, false}
	_ = c
}`
	warnings := checkUnkeyedStruct("config.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for unkeyed struct init, got none")
	}
	if !strings.Contains(warnings[0], "Unkeyed") {
		t.Errorf("expected 'Unkeyed' in warning, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "Config") {
		t.Errorf("expected type name 'Config' in warning, got: %s", warnings[0])
	}
}

func TestUnkeyedStruct_KeyedIsOK(t *testing.T) {
	src := `package main

type Config struct {
	Host string
	Port int
	TLS  bool
}

func main() {
	c := Config{Host: "localhost", Port: 8080, TLS: false}
	_ = c
}`
	warnings := checkUnkeyedStruct("config.go", "", src)
	if len(warnings) > 0 {
		t.Errorf("expected no warning for keyed struct, got: %v", warnings)
	}
}

func TestUnkeyedStruct_DeltaAware(t *testing.T) {
	oldSrc := `package main

type Server struct {
	Host string
	Port int
}

func main() {
	s := Server{"localhost", 8080}
	_ = s
}`
	newSrc := `package main

type Server struct {
	Host string
	Port int
}

type Client struct {
	Addr string
	Timeout int
}

func main() {
	s := Server{"localhost", 8080}
	c := Client{"localhost", 5000}
	_ = s
	_ = c
}`
	// Old has Server unkeyed (should be filtered out by delta).
	// New adds Client unkeyed (should be flagged).
	warnings := checkUnkeyedStruct("server.go", oldSrc, newSrc)
	if len(warnings) == 0 {
		t.Fatal("expected warning for new Client unkeyed struct")
	}
	// Should NOT flag Server (it was in old content).
	foundClient := false
	foundServer := false
	for _, w := range warnings {
		if strings.Contains(w, "Client") {
			foundClient = true
		}
		if strings.Contains(w, "Server") {
			foundServer = true
		}
	}
	if !foundClient {
		t.Error("expected warning for Client unkeyed init")
	}
	if foundServer {
		t.Error("should not flag Server (existed in old content)")
	}
}

func TestUnkeyedStruct_SliceInitIsOK(t *testing.T) {
	src := `package main

func main() {
	nums := []int{1, 2, 3}
	names := []string{"a", "b", "c"}
	_ = nums
	_ = names
}`
	warnings := checkUnkeyedStruct("slice.go", "", src)
	if len(warnings) > 0 {
		t.Errorf("expected no warning for slice init, got: %v", warnings)
	}
}

func TestUnkeyedStruct_MapInitIsOK(t *testing.T) {
	src := `package main

func main() {
	m := map[string]int{"a": 1, "b": 2}
	_ = m
}`
	warnings := checkUnkeyedStruct("map.go", "", src)
	if len(warnings) > 0 {
		t.Errorf("expected no warning for map init, got: %v", warnings)
	}
}

func TestUnkeyedStruct_QualifiedType(t *testing.T) {
	src := `package main

import "net/http"

func main() {
	h := http.Header{"Content-Type", "application/json"}
	_ = h
}`
	warnings := checkUnkeyedStruct("header.go", "", src)
	// Qualified type (http.Header) should be flagged since it's likely a struct.
	if len(warnings) == 0 {
		t.Fatal("expected warning for qualified type unkeyed init")
	}
	if !strings.Contains(warnings[0], "http.Header") {
		t.Errorf("expected 'http.Header' in warning, got: %s", warnings[0])
	}
}

func TestUnkeyedStruct_SmallStructSkipped(t *testing.T) {
	src := `package main

type Wrapper struct {
	Value int
}

func main() {
	w := Wrapper{42}
	_ = w
}`
	warnings := checkUnkeyedStruct("wrapper.go", "", src)
	if len(warnings) > 0 {
		t.Errorf("expected no warning for 1-field struct, got: %v", warnings)
	}
}

func TestUnkeyedStruct_EmptyContent(t *testing.T) {
	warnings := checkUnkeyedStruct("empty.go", "", "")
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for empty content")
	}
}

func TestUnkeyedStruct_NonGoFile(t *testing.T) {
	src := `const c = {a: 1, b: 2}`
	warnings := checkUnkeyedStruct("config.js", "", src)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for non-Go file")
	}
}

func TestUnkeyedStruct_TestFileSkipped(t *testing.T) {
	src := `package main

type Mock struct {
	A int
	B int
}

func TestMock(t *testing.T) {
	m := Mock{1, 2}
	_ = m
}`
	warnings := checkUnkeyedStruct("mock_test.go", "", src)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for test file, got: %v", warnings)
	}
}

func TestUnkeyedStruct_PartialKeyedIsOK(t *testing.T) {
	src := `package main

type Config struct {
	Host string
	Port int
	TLS  bool
}

func main() {
	// Mix of keyed and unkeyed is a compile error in Go,
	// but our checker should not panic on unparseable code.
	c := Config{Host: "localhost", 8080, false}
	_ = c
}`
	// Mixed keyed/unkeyed is invalid Go, so parser returns nil.
	// Verify no panic and graceful handling.
	warnings := checkUnkeyedStruct("mixed.go", "", src)
	if len(warnings) > 0 {
		t.Logf("got %d warnings (expected 0 for invalid Go)", len(warnings))
	}
}

func TestUnkeyedStruct_MultipleIssues(t *testing.T) {
	src := `package main

type A struct {
	X int
	Y int
	Z int
}

type B struct {
	P string
	Q string
	R string
	S string
}

func main() {
	a := A{1, 2, 3}
	b := B{"p", "q", "r", "s"}
	_ = a
	_ = b
}`
	warnings := checkUnkeyedStruct("multi.go", "", src)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 warnings, got %d", len(warnings))
	}
}

func TestUnkeyedStruct_WarningCap(t *testing.T) {
	// Generate more than unkeyedMaxWarnings issues.
	src := `package main

type S1 struct { A, B, C int }
type S2 struct { A, B, C int }
type S3 struct { A, B, C int }
type S4 struct { A, B, C int }
type S5 struct { A, B, C int }

func main() {
	_ = S1{1, 2, 3}
	_ = S2{1, 2, 3}
	_ = S3{1, 2, 3}
	_ = S4{1, 2, 3}
	_ = S5{1, 2, 3}
}`
	warnings := checkUnkeyedStruct("cap.go", "", src)
	// Should cap at unkeyedMaxWarnings + 1 truncation notice.
	if len(warnings) > unkeyedMaxWarnings+1 {
		t.Errorf("expected at most %d warnings, got %d", unkeyedMaxWarnings+1, len(warnings))
	}
}

func TestUnkeyedStruct_AllElementsUnkeyed(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		expect bool
	}{
		{"all_positional", `package m
type S struct{ A, B int }
func f() { _ = S{1, 2} }`, true},
		{"all_keyed", `package m
type S struct{ A, B int }
func f() { _ = S{A: 1, B: 2} }`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkUnkeyedStruct("test.go", "", tt.src)
			got := len(warnings) > 0
			if got != tt.expect {
				t.Errorf("expected %v, got %v (warnings: %v)", tt.expect, got, warnings)
			}
		})
	}
}
