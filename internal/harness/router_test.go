package harness

import (
	"testing"
)

// --- ExtractFeatures tests ---

func TestExtractFeatures_FilePath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Fix the bug in auth.go", true},
		{"Update src/api/user.ts to add validation", true},
		{"Refactor the main.py module", true},
		{"What is a closure?", false},
		{"Explain the architecture", false},
		{"Look at my_file.txt", false}, // .txt is not a source extension
	}
	for _, tt := range tests {
		f := ExtractFeatures(tt.input)
		if f.HasFilePath != tt.expected {
			t.Errorf("ExtractFeatures(%q).HasFilePath = %v, want %v", tt.input, f.HasFilePath, tt.expected)
		}
	}
}

func TestExtractFeatures_CodeBlock(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Fix this: ```go\nfmt.Println()```", true},
		{"What does this code do?\n```python\nprint('hi')\n```", true},
		{"Add error handling to the handler", false},
	}
	for _, tt := range tests {
		f := ExtractFeatures(tt.input)
		if f.HasCodeBlock != tt.expected {
			t.Errorf("ExtractFeatures(%q).HasCodeBlock = %v, want %v", tt.input, f.HasCodeBlock, tt.expected)
		}
	}
}

func TestExtractFeatures_ActionVerb(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Add a deleteUser method to UserService", true},
		{"Fix the null pointer exception in auth.go", true},
		{"Remove deprecated API endpoints", true},
		{"Refactor the database layer", true},
		{"Create a new config module", true},
		{"Optimize the query performance", true},
		{"What is a closure?", false},
		{"Explain the architecture", false},
		{"Why does this test fail?", false},
		// Chinese
		{"添加一个删除用户的方法", true},
		{"修复空指针异常", true},
		{"重构数据库层", true},
	}
	for _, tt := range tests {
		f := ExtractFeatures(tt.input)
		if f.HasActionVerb != tt.expected {
			t.Errorf("ExtractFeatures(%q).HasActionVerb = %v, want %v", tt.input, f.HasActionVerb, tt.expected)
		}
	}
}

func TestExtractFeatures_QuestionOnly(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"What is a closure?", true},
		{"Why does this test fail?", true},
		{"Explain the architecture?", true},
		{"How does OAuth work?", true},
		{"什么是闭包？", true},        // Chinese question mark
		{"Fix the bug?", false}, // Has action verb
		{"Add a method", false}, // Not a question
		{"Fix the null pointer exception in auth.go", false},
	}
	for _, tt := range tests {
		f := ExtractFeatures(tt.input)
		if f.IsQuestionOnly != tt.expected {
			t.Errorf("ExtractFeatures(%q).IsQuestionOnly = %v, want %v", tt.input, f.IsQuestionOnly, tt.expected)
		}
	}
}

func TestExtractFeatures_TaskGoal(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Add a deleteUser method to UserService", true},
		{"Fix the null pointer exception in auth.go", true},
		{"Create test coverage for the API module", true},
		{"Hello", false},
		{"What?", false},
	}
	for _, tt := range tests {
		f := ExtractFeatures(tt.input)
		if f.HasTaskGoal != tt.expected {
			t.Errorf("ExtractFeatures(%q).HasTaskGoal = %v, want %v", tt.input, f.HasTaskGoal, tt.expected)
		}
	}
}

func TestExtractFeatures_TooShort(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Hi", true},
		{"Fix", true},
		{"OK", true},
		{"", true},
		{"Fix the bug", false}, // 11 chars
	}
	for _, tt := range tests {
		f := ExtractFeatures(tt.input)
		if f.IsTooShort != tt.expected {
			t.Errorf("ExtractFeatures(%q).IsTooShort = %v, want %v", tt.input, f.IsTooShort, tt.expected)
		}
	}
}

func TestExtractFeatures_ExplicitExclude(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Don't change anything, just explain the code", true},
		{"Do not change the file, only explain", true},
		{"Just explain this function", true},
		{"Only analyze the error", true},
		{"不要修改代码", true},
		{"只是解释一下", true},
		{"Fix the bug in auth.go", false},
		{"Add error handling", false},
	}
	for _, tt := range tests {
		f := ExtractFeatures(tt.input)
		if f.ExplicitExclude != tt.expected {
			t.Errorf("ExtractFeatures(%q).ExplicitExclude = %v, want %v", tt.input, f.ExplicitExclude, tt.expected)
		}
	}
}

// --- DecideRouteWithFeatures tests ---

func TestDecideRouteWithFeatures_SelectionBoost(t *testing.T) {
	// "Optimize this" is ambiguous normally (score=2),
	// but with HasSelection + action verb → should route
	input := "Optimize this"
	features := ExtractFeatures(input)

	// Without selection: too ambiguous
	ctx := RouteContext{HasSelection: false}
	got := DecideRouteWithFeatures(input, "on", features, ctx)
	if got != RouteNormal {
		t.Errorf("without selection: got %v, want RouteNormal", got)
	}

	// With selection + action verb: should route
	ctx = RouteContext{HasSelection: true}
	got = DecideRouteWithFeatures(input, "on", features, ctx)
	if got != RouteHarness {
		t.Errorf("with selection: got %v, want RouteHarness", got)
	}
}

func TestDecideRouteWithFeatures_QuestionWithSelection(t *testing.T) {
	// "Why does this test fail?" with selection — still a question
	input := "Why does this test fail?"
	features := ExtractFeatures(input)
	ctx := RouteContext{HasSelection: true}

	got := DecideRouteWithFeatures(input, "on", features, ctx)
	if got != RouteNormal {
		t.Errorf("question with selection: got %v, want RouteNormal", got)
	}
}

// --- RouteDecision.String test ---

func TestRouteDecisionString(t *testing.T) {
	tests := []struct {
		d    RouteDecision
		want string
	}{
		{RouteNone, "none"},
		{RouteNormal, "normal"},
		{RouteSuggest, "suggest"},
		{RouteHarness, "harness"},
		{RouteDecision(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.d.String()
		if got != tt.want {
			t.Errorf("RouteDecision(%d).String() = %q, want %q", tt.d, got, tt.want)
		}
	}
}
