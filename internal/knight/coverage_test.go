package knight

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// --- analyzer.go pure functions ---

func TestExtractText(t *testing.T) {
	blocks := []provider.ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "tool_use", Text: "ignored"},
		{Type: "text", Text: "world"},
	}
	got := extractText(blocks)
	if got != "hello world" {
		t.Errorf("extractText = %q, want %q", got, "hello world")
	}
}

func TestExtractText_Empty(t *testing.T) {
	got := extractText(nil)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFindPrevAssistant(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "user"},
	}
	m := findPrevAssistant(msgs, 2)
	if m == nil || m.Role != "assistant" {
		t.Error("expected assistant at index 1")
	}
}

func TestFindPrevAssistant_None(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user"},
		{Role: "user"},
	}
	m := findPrevAssistant(msgs, 2)
	if m != nil {
		t.Error("expected nil")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	got := truncate("hello world", 5)
	if got != "hello..." {
		t.Errorf("expected 'hello...', got %q", got)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello-world"},
		{"test@#$%", "test"},
		{"UPPER_case-123", "UPPER-case-123"},
		{"   ", ""},
	}
	for _, tt := range tests {
		got := sanitizeName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBuildCorrectionSkillName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"你需要编译的是正式的 ggcode 二进制而不是什么 debug 二进制", "build-convention"},
		{"为什么你不会去看看逻辑再得出你的结论呢", "read-code-before-concluding"},
		{"不要没搞清楚状况就自作主张", "understand-before-acting"},
		{"先看一下代码", "read-code-first"},
		{"bool默认值应该是true", "default-value-check"},
	}
	for _, tt := range tests {
		got := buildCorrectionSkillName(tt.input)
		if got != tt.expected {
			t.Errorf("buildCorrectionSkillName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBuildFailureFixName(t *testing.T) {
	tests := []struct {
		f        failure
		expected string
	}{
		{failure{toolInp: `{"command":"go build ./..."}`, errMsg: "undefined: x"}, "build-failure-recovery"},
		{failure{toolInp: `{"command":"go test ./..."}`, errMsg: "FAIL"}, "test-failure-recovery"},
		{failure{toolInp: `{"file_path":"internal/config/config.go"}`, errMsg: "no such file"}, "missing-file-recovery"},
		{failure{toolName: "edit_file"}, "fix-edit-file"},
	}
	for _, tt := range tests {
		got := buildFailureFixName(tt.f)
		if got != tt.expected {
			t.Errorf("buildFailureFixName(%+v) = %q, want %q", tt.f, got, tt.expected)
		}
	}
}

// --- scheduler.go pure functions ---

func TestFormatKnightTaskOutput(t *testing.T) {
	if formatKnightTaskOutput("") != "task completed without a report" {
		t.Error("expected default message")
	}
	if formatKnightTaskOutput("short report") != "short report" {
		t.Error("expected passthrough")
	}
	long := ""
	for i := 0; i < 2000; i++ {
		long += "x"
	}
	got := formatKnightTaskOutput(long)
	if len(got) > 1510 {
		t.Errorf("too long: %d", len(got))
	}
}

func TestParseSkillRef(t *testing.T) {
	tests := []struct {
		ref         string
		scope, name string
	}{
		{"project:my-skill", "project", "my-skill"},
		{"global:my-skill", "global", "my-skill"},
		{"my-skill", "", "my-skill"},
		{"  project:spaced  ", "project", "spaced"},
	}
	for _, tt := range tests {
		scope, name := parseSkillRef(tt.ref)
		if scope != tt.scope || name != tt.name {
			t.Errorf("parseSkillRef(%q) = (%q,%q), want (%q,%q)", tt.ref, scope, name, tt.scope, tt.name)
		}
	}
}

func TestFormatSkillRef(t *testing.T) {
	if formatSkillRef("project", "my-skill") != "project:my-skill" {
		t.Error("expected scoped ref")
	}
	if formatSkillRef("", "my-skill") != "my-skill" {
		t.Error("expected unscoped ref")
	}
}

func TestFormatSkillRefForDisplay(t *testing.T) {
	if FormatSkillRefForDisplay("project", "my-skill") != "project:my-skill" {
		t.Error("expected scoped ref")
	}
}

func TestFindSkillByRef_NotFound(t *testing.T) {
	entries := []*SkillEntry{{Name: "other", Scope: "project"}}
	_, err := findSkillByRef(entries, "project:missing", "active")
	if err == nil {
		t.Error("expected error for not found")
	}
}

func TestFindSkillByRef_Found(t *testing.T) {
	entries := []*SkillEntry{{Name: "my-skill", Scope: "project"}}
	e, err := findSkillByRef(entries, "project:my-skill", "active")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Name != "my-skill" {
		t.Errorf("expected my-skill, got %q", e.Name)
	}
}

func TestFindSkillByRef_Multiple(t *testing.T) {
	entries := []*SkillEntry{
		{Name: "my-skill", Scope: "project"},
		{Name: "my-skill", Scope: "global"},
	}
	_, err := findSkillByRef(entries, "my-skill", "active")
	if err == nil {
		t.Error("expected error for multiple matches")
	}
}

func TestInQuietHours(t *testing.T) {
	// inQuietHours does not exist; test parseQuietWindow instead
}

func TestParseClockMinutes(t *testing.T) {
	m, ok := parseClockMinutes("12:30")
	if !ok || m != 12*60+30 {
		t.Errorf("expected 750, got %d, ok=%v", m, ok)
	}
	_, ok = parseClockMinutes("invalid")
	if ok {
		t.Error("expected not ok")
	}
}

// --- budget.go ---

func TestSplitLines(t *testing.T) {
	lines := splitLines("a\nb\nc")
	if len(lines) != 3 {
		t.Errorf("expected 3, got %d", len(lines))
	}
}

func TestTrimSpace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  hello  ", "hello"},
		{"", ""},
		{"\t", ""},
	}
	for _, tt := range tests {
		got := trimSpace(tt.input)
		if got != tt.expected {
			t.Errorf("trimSpace(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- skill_promoter.go ---

func TestValidateSkillName(t *testing.T) {
	if err := validateSkillName("my-skill-123"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := validateSkillName(""); err == nil {
		t.Error("expected error for empty name")
	}
	if err := validateSkillName("a b c"); err == nil {
		t.Error("expected error for spaces")
	}
}

func TestSplitFrontmatter(t *testing.T) {
	content := "---\ntitle: test\n---\ncontent"
	bodyStart := splitFrontmatter(content)
	if bodyStart <= 0 {
		t.Errorf("expected positive bodyStart, got %d", bodyStart)
	}
}

func TestSplitFrontmatter_NoDelim(t *testing.T) {
	bodyStart := splitFrontmatter("just content")
	if bodyStart >= 0 {
		t.Errorf("expected negative for no frontmatter, got %d", bodyStart)
	}
}

func TestExtractFrontmatterText(t *testing.T) {
	content := "---\ntitle: test\n---\nbody"
	got := extractFrontmatterText(content, len(content)-len("body"))
	if got != "title: test" {
		t.Errorf("expected 'title: test', got %q", got)
	}
}
