package agent

import (
	"strings"
	"testing"
)

func TestSymbolGrounding_Reset(t *testing.T) {
	s := newSymbolGroundingState()
	s.addGrounded("testFunc")
	s.warnCount = 1
	s.reset()
	if len(s.grounded) != 0 {
		t.Fatalf("expected grounded map to be empty after reset, got %d", len(s.grounded))
	}
	if s.warnCount != 0 {
		t.Fatalf("expected warnCount 0, got %d", s.warnCount)
	}
}

func TestSymbolGrounding_RecordGrounding(t *testing.T) {
	s := newSymbolGroundingState()
	// Tool input with file paths
	input := `{"path": "internal/agent/foo.go", "old_text": "bar"}`
	result := "func processPayment() error {\n\treturn nil\n}\n"

	s.recordGrounding(input, result)

	if !s.isGrounded("internal/agent/foo.go") {
		t.Error("expected file path to be grounded")
	}
	if !s.isGrounded("foo.go") {
		t.Error("expected base file name to be grounded")
	}
	if !s.isGrounded("processPayment") {
		t.Error("expected function name from result to be grounded")
	}
}

func TestSymbolGrounding_IsGrounded(t *testing.T) {
	s := newSymbolGroundingState()
	s.addGrounded("myFunction")
	s.addGrounded("MyType")

	tests := []struct {
		input    string
		expected bool
	}{
		{"myFunction", true},
		{"MyFunction", true}, // case-insensitive
		{"MYFUNCTION", true},
		{"MyType", true},
		{"unknownFunc", false},
		{"", false},
	}

	for _, tt := range tests {
		got := s.isGrounded(tt.input)
		if got != tt.expected {
			t.Errorf("isGrounded(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestSymbolGrounding_AddBounded(t *testing.T) {
	s := newSymbolGroundingState()
	s.grounded = nil // simulate nil map
	s.addGrounded("test")
	if !s.isGrounded("test") {
		t.Error("expected addGrounded to init nil map and add symbol")
	}
}

func TestSymbolGrounding_ShortSymbolIgnored(t *testing.T) {
	s := newSymbolGroundingState()
	s.addGrounded("ab") // too short (<3)
	if s.isGrounded("ab") {
		t.Error("expected short symbol to be ignored")
	}
}

func TestExtractGroundedPaths(t *testing.T) {
	input := `{"path": "src/main.go", "file_path": "lib/util.ts", "other": "value"}`
	paths := extractGroundedPaths(input)
	found := make(map[string]bool)
	for _, p := range paths {
		found[p] = true
	}
	if !found["src/main.go"] {
		t.Error("expected src/main.go in paths")
	}
	if !found["lib/util.ts"] {
		t.Error("expected lib/util.ts in paths")
	}
}

func TestExtractGroundedIdents(t *testing.T) {
	result := "func HandleRequest() error {\n\treturn nil\n}\ntype Server struct {}\n"
	idents := extractGroundedIdents(result)
	found := make(map[string]bool)
	for _, id := range idents {
		found[id] = true
	}
	if !found["HandleRequest"] {
		t.Error("expected HandleRequest in idents")
	}
	if !found["Server"] {
		t.Error("expected Server in idents")
	}
}

func TestIsCommonPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"node_modules/react/index.js", true},
		{"vendor/github.com/pkg/errors.go", true},
		{".git/config", true},
		{"src/main.go", false},
		{"internal/agent/agent.go", false},
	}
	for _, tt := range tests {
		got := isCommonPath(tt.path)
		if got != tt.expected {
			t.Errorf("isCommonPath(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}

func TestIsPlausibleSymbol(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"getUserById", true},
		{"PaymentService", true},
		{"ab", false}, // too short
		{"", false},
		{"---", false},                   // no alnum
		{strings.Repeat("a", 65), false}, // too long
	}
	for _, tt := range tests {
		got := isPlausibleSymbol(tt.input)
		if got != tt.expected {
			t.Errorf("isPlausibleSymbol(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
