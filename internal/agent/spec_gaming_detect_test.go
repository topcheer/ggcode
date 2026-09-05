package agent

import (
	"testing"
)

func TestSpecGaming_OnlyTestFilesEdited(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{"internal/agent/foo_test.go"},
	}

	msg := a.checkSpecGaming(stats, "fix the authentication bug")
	if msg == "" {
		t.Fatal("expected spec gaming warning for test-only edits")
	}
}

func TestSpecGaming_SourceAndTestFilesEdited(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{"internal/agent/foo.go", "internal/agent/foo_test.go"},
	}

	msg := a.checkSpecGaming(stats, "fix the authentication bug")
	if msg != "" {
		t.Fatalf("expected no spec gaming warning when source is edited, got: %s", msg)
	}
}

func TestSpecGaming_SkipMarkersInCommands(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{"src/auth.go"},
		CommandsRun: []string{`echo "@pytest.mark.skip" >> auth_test.py`},
	}

	// Skip markers in commands should trigger even with source edits
	msg := a.checkSpecGaming(stats, "fix auth bug")
	if msg == "" {
		t.Fatal("expected spec gaming warning for skip markers in commands")
	}
}

func TestSpecGaming_CIConfigTampering(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{".github/workflows/ci.yml", "src/main.go"},
	}

	msg := a.checkSpecGaming(stats, "add user registration feature")
	if msg == "" {
		t.Fatal("expected spec gaming warning for CI config tampering")
	}
}

func TestSpecGaming_CIConfigInCITask(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{".github/workflows/ci.yml"},
	}

	// CI task should not trigger warning
	msg := a.checkSpecGaming(stats, "update CI pipeline to use Go 1.26")
	if msg != "" {
		t.Fatalf("expected no spec gaming warning for CI-related task, got: %s", msg)
	}
}

func TestSpecGaming_FiresOncePerRun(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{"foo_test.go"},
	}

	msg1 := a.checkSpecGaming(stats, "fix bug")
	if msg1 == "" {
		t.Fatal("expected first call to detect spec gaming")
	}

	msg2 := a.checkSpecGaming(stats, "fix bug")
	if msg2 != "" {
		t.Fatal("expected second call to be suppressed (already fired)")
	}
}

func TestSpecGaming_Reset(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{"foo_test.go"},
	}

	// Fire once
	_ = a.checkSpecGaming(stats, "fix bug")
	// Reset should allow firing again
	a.specGaming.reset()
	msg := a.checkSpecGaming(stats, "fix bug")
	if msg == "" {
		t.Fatal("expected spec gaming to fire after reset")
	}
}

func TestSpecGaming_NoEdits(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	stats := &RunStats{
		FilesEdited: []string{},
	}

	msg := a.checkSpecGaming(stats, "fix bug")
	if msg != "" {
		t.Fatalf("expected no warning with no edits, got: %s", msg)
	}
}

func TestSpecGamingIsTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"foo_test.go", true},
		{"bar_test.py", true},
		{"utils.test.ts", true},
		{"component.spec.tsx", true},
		{"foo.go", false},
		{"main.py", false},
		{"README.md", false},
	}
	for _, tt := range tests {
		got := specGamingIsTestFile(tt.path)
		if got != tt.want {
			t.Errorf("specGamingIsTestFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestStripTestSuffix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"foo/bar_test.go", "foo/bar.go"},
		{"utils.test.ts", "utils.ts"},
		{"component.spec.tsx", "component.tsx"},
		{"foo_test.py", "foo.py"},
	}
	for _, tt := range tests {
		got := stripTestSuffix(tt.input)
		if got != tt.want {
			t.Errorf("stripTestSuffix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsCIConfigPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".github/workflows/ci.yml", true},
		{"conftest.py", true},
		{"jest.config.js", true},
		{"src/main.go", false},
		{"pytest.ini", true},
	}
	for _, tt := range tests {
		got := isCIConfigPath(tt.path)
		if got != tt.want {
			t.Errorf("isCIConfigPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestHasSkipMarkersInCommands(t *testing.T) {
	tests := []struct {
		commands []string
		want     bool
	}{
		{[]string{"go test ./..."}, false},
		{[]string{"sed -i 's/TestAuth//' auth_test.go"}, false},
		{[]string{"echo '@pytest.mark.skip'"}, true},
		{[]string{"echo 't.Skip('"}, true},
	}
	for _, tt := range tests {
		got := hasSkipMarkersInCommands(tt.commands)
		if got != tt.want {
			t.Errorf("hasSkipMarkersInCommands(%v) = %v, want %v", tt.commands, got, tt.want)
		}
	}
}

func TestIsCIRelatedTask(t *testing.T) {
	tests := []struct {
		prompt string
		want   bool
	}{
		{"update the CI pipeline", true},
		{"fix authentication bug", false},
		{"modify the Makefile build target", true},
		{"add new feature to user handler", false},
		{"configure jest.config.js", true},
		// #501: everyday words CONTAINING the substring "ci" must NOT
		// disable the CI-tampering pattern (old bare-substring match hit
		// 12/12 ordinary prompts empirically).
		{"fix the precision issue in the pricing calculator", false},
		{"make the lookup more efficient", false},
		{"this is a special case, decide carefully", false},
		{"sufficient artificial precision, crucial decision", false},
		{"run it in ci", true}, // whole-word (case-insensitive) still matches
	}
	for _, tt := range tests {
		got := isCIRelatedTask(tt.prompt)
		if got != tt.want {
			t.Errorf("isCIRelatedTask(%q) = %v, want %v", tt.prompt, got, tt.want)
		}
	}
}

func TestSpecGaming_MultipleWarnings(t *testing.T) {
	a := &Agent{
		specGaming: newSpecGamingState(),
	}
	// Only test files + skip markers + CI tampering = 3 warnings
	stats := &RunStats{
		FilesEdited: []string{"foo_test.go", ".github/workflows/ci.yml"},
		CommandsRun: []string{"echo '@pytest.mark.skip'"},
	}

	msg := a.checkSpecGaming(stats, "fix the bug")
	if msg == "" {
		t.Fatal("expected spec gaming warning")
	}
	// Should contain multiple warnings
	if len(msg) < 100 {
		t.Logf("warning message: %s", msg)
	}
}

// Regression for #1496: the awk branch used to check skip markers against
// the WHOLE command, so an injection (gsub(/assert/, "t.Skip(") - marker in
// the REPLACEMENT) was exempted as "removal". The fix mirrors the sed
// branch: pattern contains the marker, replacement does not.
func TestIsAwkSkipRemovalInjectionNotExempt(t *testing.T) {
	// Injection: marker lives in the replacement -> NOT removal.
	if isAwkSkipRemoval(`awk '{gsub(/assert/, "t.Skip(")}' t.go`) {
		t.Fatal("gsub injection (skip marker in replacement) must not be exempted as removal")
	}
	// Legit removal: marker in pattern, clean replacement -> exempt.
	if !isAwkSkipRemoval(`awk '{gsub(/t\.skip\(/, "")}' t.go`) {
		t.Fatal("gsub removing a skip marker (pattern only) must stay exempt")
	}
	// No marker at all.
	if isAwkSkipRemoval(`awk '{gsub(/foo/, "bar")}' t.go`) {
		t.Fatal("no skip marker anywhere must not match")
	}
}
