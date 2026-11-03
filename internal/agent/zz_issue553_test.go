package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue553_C_VerifyTokenMustBeInCommandPosition(t *testing.T) {

	bareArgCmds := []string{
		"grep -n test main.go",
		"grep -rn build .",
		"cat lint_config.yaml",
		"ls -la verify results/",
		"git diff check",
		"cp test /tmp/",
		"echo vet compile check",
		"tail -f build.log",
		"rm -rf test build lint",
		"grep --include=check-* .",
		"find . -name test_*.go",
		"sort verify.txt | uniq",
		"go run lint_script.go",
		"cargo run build_tool.rs",
	}
	for _, c := range bareArgCmds {
		if psIsVerifyCommand(c) {
			t.Errorf("psIsVerifyCommand(%q) = true — argument-position token must not arm everVerified (#553)", c)
		}
	}

	trueCmds := []string{
		"go test ./...",
		"go build ./...",
		"go vet ./...",
		"npm test",
		"make test",
		"pytest -v",
		"cargo test",
		"make lint",
		"npm run build",
		"cmake --build .",
		"test",
		"check-all",
		"cd pkg && go test ./...",
		"go build ./... && go vet ./...",
		"cd pkg; make test",
		"echo setup | grep -v test && go test ./...",
		"uv run pytest",
		"cargo build --release",
		"flutter test",
		"python -m pytest tests",
	}
	for _, c := range trueCmds {
		if !psIsVerifyCommand(c) {
			t.Errorf("psIsVerifyCommand(%q) = false — expected verification", c)
		}
	}
}

func TestIssue553_C_E2E_GrepFilenameDoesNotArmEverVerified(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false, "")

	s.recordToolCall("run_command", map[string]interface{}{"command": "grep -n test main.go"}, false, "")

	if s.everVerified {
		t.Fatal("grep with 'test' filename argument must NOT set everVerified")
	}
	if hint := s.checkSuccessClaim("All tests pass."); hint == "" {
		t.Fatal("detector was silenced by filename-argument false positive (#553)")
	}
}

func TestIssue553_C_CommandPositionTokens(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
		bad  []string
	}{
		{"grep -n test main.go", []string{"grep"}, []string{"test", "main.go"}},
		{"go test ./...", []string{"go", "test"}, []string{"./..."}},
		{"cd pkg && make test", []string{"cd", "make", "test"}, nil},
		{"echo a | grep b", []string{"echo", "grep"}, []string{"a", "b"}},
		{"python -m pytest tests", []string{"python", "pytest"}, []string{"tests"}},
	}
	for _, c := range cases {
		got := psCommandPositionTokens(strings.Fields(strings.ToLower(c.cmd)))
		for _, w := range c.want {
			if !got[w] {
				t.Errorf("psCommandPositionTokens(%q): %q should be command position, got %v", c.cmd, w, got)
			}
		}
		for _, b := range c.bad {
			if got[b] {
				t.Errorf("psCommandPositionTokens(%q): %q should NOT be command position, got %v", c.cmd, b, got)
			}
		}
	}
}

func TestIssue553_A1_HintTextNotMetered(t *testing.T) {
	s := newTokenWasteBudgetState()

	original := "x"
	polluted := original + "\n\n[some-guidance] " + strings.Repeat("hint text ", 8)

	s.recordToolResultLen("run_command", polluted, len(original), false, false, nil)
	if len(s.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(s.entries))
	}
	if got := s.entries[0].tokens; got != estimateTokens(original) {
		t.Errorf("metered tokens = %d, want %d (original content only) — hint text must not be counted (#553 A1)", got, estimateTokens(original))
	}
	if s.totalTokens != estimateTokens(original) {
		t.Errorf("totalTokens = %d, want %d", s.totalTokens, estimateTokens(original))
	}
}

func TestIssue553_A1_LegacyRecordStillMetersFullContent(t *testing.T) {
	s := newTokenWasteBudgetState()
	content := strings.Repeat("z", 400)
	s.recordToolResult("read_file", content, false, false, nil)
	if s.entries[0].tokens != estimateTokens(content) {
		t.Errorf("legacy recordToolResult metered %d, want %d", s.entries[0].tokens, estimateTokens(content))
	}
}

func TestIssue553_A1_OverrideNegativeLenFallsBack(t *testing.T) {
	content := strings.Repeat("y", 300)
	s := newTokenWasteBudgetState()
	s.recordToolResultLen("grep", content, -1, false, false, nil)
	if s.entries[0].tokens != estimateTokens(content) {
		t.Errorf("override -1 should fall back to len(content): got %d, want %d", s.entries[0].tokens, estimateTokens(content))
	}
}

func TestIssue553_A2_NegativeMarkersWordBoundary(t *testing.T) {
	notNegative := []string{
		"unclean working tree",
		"nothing noteworthy",
		"found 3 unclean files",
		"rechecked nothing found",
	}

	for _, c := range notNegative[:3] {
		if isNegativeResult(c) {
			t.Errorf("isNegativeResult(%q) = true — marker must match at word start (#553 A2)", c)
		}
	}
	if !isNegativeResult(notNegative[3]) {
		t.Errorf("isNegativeResult(%q) = false — exact marker must still match", notNegative[3])
	}

	negative := []string{
		"nothing found",
		"no matches found for FooBar",
		"working tree clean",
		"clean",
		"No results",
		"up to date",
		"0 findings",
	}
	for _, c := range negative {
		if !isNegativeResult(c) {
			t.Errorf("isNegativeResult(%q) = false — genuine negative marker must match", c)
		}
	}
}

func TestIssue553_A2_UncleanShortResultCountsAsEmpty(t *testing.T) {
	s := newTokenWasteBudgetState()
	before := s.catTotals[wasteEmpty]
	s.recordToolResult("git_status", "unclean working tree", false, false, nil)
	if s.catTotals[wasteEmpty] == before {
		t.Error("'unclean working tree' must no longer be exempt from wasteEmpty (#553 A2)")
	}
}

func TestIssue553_A3_AbsoluteReadRelativeEditExpires(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot determine wd: %v", err)
	}
	s := newTokenWasteBudgetState()
	absPath := wd + "/internal/agent/zz_issue553_test.go"
	relPath := "./internal/agent/zz_issue553_test.go"

	s.recordToolResult("read_file", strings.Repeat("code", 500), false, false, []string{absPath})
	if s.wasteTokens != 0 {
		t.Fatalf("expected 0 waste before edit, got %d", s.wasteTokens)
	}

	s.markFileEdited(relPath)
	if s.wasteTokens == 0 {
		t.Error("relative-path edit must expire the absolute-path read (#553 A3)")
	}
	if s.catTotals[wasteExpired] == 0 {
		t.Error("expected wasteExpired tokens after cross-format edit")
	}
}

func TestIssue553_A3_RelativeReadAbsoluteEditExpires(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot determine wd: %v", err)
	}
	s := newTokenWasteBudgetState()
	relPath := "internal/agent/premature_success.go"
	absPath := wd + "/" + relPath

	s.recordToolResult("read_file", strings.Repeat("code", 500), false, false, []string{relPath})
	s.markFileEdited(absPath)
	if s.wasteTokens == 0 {
		t.Error("absolute-path edit must expire the relative-path read (#553 A3)")
	}
}

func TestIssue553_A3_RedundantSlashAndDotNormalized(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot determine wd: %v", err)
	}
	s := newTokenWasteBudgetState()
	messy := wd + "/./internal//agent/../agent/premature_success.go"
	s.recordToolResult("read_file", strings.Repeat("code", 500), false, false, []string{messy})
	s.markFileEdited(wd + "/internal/agent/premature_success.go")
	if s.wasteTokens == 0 {
		t.Error("non-normalized path keys must still match after cleaning (#553 A3)")
	}
}

func TestIssue553_A3_DifferentFilesStillDistinct(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot determine wd: %v", err)
	}
	s := newTokenWasteBudgetState()
	s.recordToolResult("read_file", strings.Repeat("code", 500), false, false, []string{wd + "/internal/agent/agent.go"})
	s.markFileEdited("internal/agent/premature_success.go")
	if s.wasteTokens != 0 {
		t.Error("edit to a different file must not expire the read (#553 A3)")
	}
}

func TestIssue553_A3_WastePathKeyUnits(t *testing.T) {
	if got := wastePathKey(""); got != "" {
		t.Errorf("wastePathKey(\"\") = %q, want empty", got)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot determine wd: %v", err)
	}
	if got := wastePathKey("./x/../y/./z.go"); got != wd+"/y/z.go" {
		t.Errorf("wastePathKey normalized to %q, want %q", got, wd+"/y/z.go")
	}
	if got := wastePathKey(wd + "/a//b.go"); got != wd+"/a/b.go" {
		t.Errorf("wastePathKey double-slash = %q, want %q", got, wd+"/a/b.go")
	}
}
