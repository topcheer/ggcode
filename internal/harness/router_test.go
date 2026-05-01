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

// --- DecideRoute tests ---

func TestDecideRoute_OffMode(t *testing.T) {
	// Any input with off mode returns RouteNone
	tests := []string{
		"Fix the bug in auth.go",
		"Add a deleteUser method",
		"Refactor the API layer",
	}
	for _, input := range tests {
		got := DecideRoute(input, "off")
		if got != RouteNone {
			t.Errorf("DecideRoute(%q, off) = %v, want RouteNone", input, got)
		}
	}
}

func TestDecideRoute_SlashCommands(t *testing.T) {
	tests := []string{
		"/harness run",
		"/status",
		"/help",
	}
	for _, input := range tests {
		for _, mode := range []string{"suggest", "on", "strict"} {
			got := DecideRoute(input, mode)
			if got != RouteNormal {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteNormal", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_ShellDirectives(t *testing.T) {
	tests := []string{
		"$ git status",
		"> ls -la",
	}
	for _, input := range tests {
		for _, mode := range []string{"suggest", "on", "strict"} {
			got := DecideRoute(input, mode)
			if got != RouteNormal {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteNormal", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_Questions(t *testing.T) {
	// Pure questions should not be routed
	tests := []string{
		"What is a closure?",
		"Why does this test fail?",
		"Explain the architecture?",
		"How does OAuth work?",
	}
	for _, input := range tests {
		for _, mode := range []string{"suggest", "on", "strict"} {
			got := DecideRoute(input, mode)
			if got != RouteNormal {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteNormal", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_ExplicitExclude(t *testing.T) {
	tests := []string{
		"Don't change anything, just explain auth.go",
		"Just explain this function in user.ts",
	}
	for _, input := range tests {
		for _, mode := range []string{"suggest", "on", "strict"} {
			got := DecideRoute(input, mode)
			if got != RouteNormal {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteNormal", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_TooShort(t *testing.T) {
	tests := []string{
		"Hi",
		"Fix",
		"",
	}
	for _, input := range tests {
		for _, mode := range []string{"suggest", "on", "strict"} {
			got := DecideRoute(input, mode)
			if got != RouteNormal {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteNormal", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_CodeTasks_SuggestMode(t *testing.T) {
	// Clear code-change tasks with suggest mode → RouteSuggest
	tests := []string{
		"Fix the null pointer exception in auth.go",
		"Add a deleteUser method to UserService",
		"Remove deprecated API endpoints from handler.go",
		"Refactor the database layer in db/postgres.py",
		"Create unit tests for the auth module",
		"Update the config handler to support YAML",
		"Implement error handling for the API endpoint in routes.ts",
	}
	for _, input := range tests {
		got := DecideRoute(input, "suggest")
		if got != RouteSuggest {
			t.Errorf("DecideRoute(%q, suggest) = %v, want RouteSuggest", input, got)
		}
	}
}

func TestDecideRoute_CodeTasks_OnMode(t *testing.T) {
	// Clear code-change tasks with on/strict mode → RouteHarness
	tests := []string{
		"Fix the null pointer exception in auth.go",
		"Add a deleteUser method to UserService",
		"Remove deprecated API endpoints from handler.go",
		"Refactor the database layer in db/postgres.py",
	}
	for _, input := range tests {
		for _, mode := range []string{"on", "strict"} {
			got := DecideRoute(input, mode)
			if got != RouteHarness {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteHarness", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_AmbiguousTasks(t *testing.T) {
	// Tasks with only action verb, no file path, and minimal goal signals.
	// "Optimize this" and "Fix it" score 2 (verb only) → below threshold.
	// "Improve the code" scores 3 (verb + "the " goal indicator) → routes.
	ambiguous := []string{
		"Optimize this",
		"Fix it",
	}
	routing := []string{
		"Improve the code",
	}
	for _, input := range ambiguous {
		for _, mode := range []string{"on", "strict", "suggest"} {
			got := DecideRoute(input, mode)
			if got != RouteNormal {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteNormal (ambiguous)", input, mode, got)
			}
		}
	}
	for _, input := range routing {
		for _, mode := range []string{"on", "strict"} {
			got := DecideRoute(input, mode)
			if got != RouteHarness {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteHarness (verb+goal)", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_VerbWithGoal(t *testing.T) {
	// Verb + goal = score 3 (verb=2 + goal=1), should route
	tests := []string{
		"Add error handling for the API endpoint",
		"Create test coverage for the module",
		"Fix the memory leak in the handler",
	}
	for _, input := range tests {
		got := DecideRoute(input, "on")
		if got != RouteHarness {
			t.Errorf("DecideRoute(%q, on) = %v, want RouteHarness", input, got)
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

// --- IsFilePath test ---

func TestIsFilePath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"auth.go", true},
		{"src/api/user.ts", true},
		{"config.yaml", true},
		{"README.md", true},
		{"data.txt", false},
		{"image.png", false},
		{"noextension", false},
	}
	for _, tt := range tests {
		got := IsFilePath(tt.input)
		if got != tt.expected {
			t.Errorf("IsFilePath(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// --- Chinese input tests ---

func TestDecideRoute_ChineseInputs(t *testing.T) {
	tests := []struct {
		input string
		mode  string
		want  RouteDecision
	}{
		{"修复 auth.go 里的空指针 bug", "on", RouteHarness},
		{"添加一个删除用户的方法", "on", RouteHarness},
		{"重构数据库层", "suggest", RouteNormal}, // verb only, no file/goal → ambiguous
		{"什么是闭包？", "on", RouteNormal},
		{"不要修改代码", "on", RouteNormal},
		{"只是解释一下这个函数", "on", RouteNormal},
	}
	for _, tt := range tests {
		got := DecideRoute(tt.input, tt.mode)
		if got != tt.want {
			t.Errorf("DecideRoute(%q, %s) = %v, want %v", tt.input, tt.mode, got, tt.want)
		}
	}
}

// --- Edge case tests ---

func TestDecideRoute_EmptyInput(t *testing.T) {
	got := DecideRoute("", "on")
	if got != RouteNormal {
		t.Errorf("DecideRoute('', on) = %v, want RouteNormal", got)
	}
}

func TestDecideRoute_WhitespaceInput(t *testing.T) {
	got := DecideRoute("   \t\n  ", "on")
	if got != RouteNormal {
		t.Errorf("DecideRoute(whitespace, on) = %v, want RouteNormal", got)
	}
}

func TestDecideRoute_MixedCaseMode(t *testing.T) {
	input := "Fix the bug in auth.go"
	for _, mode := range []string{"ON", "On", "oS", "STRICT", "Strict"} {
		got := DecideRoute(input, mode)
		// Only lowercase "on" and "strict" should route
		if mode == "ON" || mode == "STRICT" {
			if got != RouteHarness {
				t.Errorf("DecideRoute(%q, %s) = %v, want RouteHarness", input, mode, got)
			}
		}
	}
}

func TestDecideRoute_UnknownMode(t *testing.T) {
	input := "Fix the bug in auth.go"
	got := DecideRoute(input, "unknown")
	// Unknown mode is treated as "off" → RouteNone
	if got != RouteNone {
		t.Errorf("DecideRoute(%q, unknown) = %v, want RouteNone", input, got)
	}
}
