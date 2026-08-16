package agent

// Feature tests for GitHub issue #553 (ver-40 probe findings; #483's fix was
// half-done). One test per bug: C, A1, A2, A3.

import (
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Bug C: psIsVerifyCommand bare-token matching must be command-position only.
// ---------------------------------------------------------------------------

// TestIssue553_C_VerifyTokenMustBeInCommandPosition: a bare verify-verb token
// at an ARGUMENT position (`grep -n test main.go` — "test" is a filename)
// must NOT arm everVerified; the same verbs in command position must.
func TestIssue553_C_VerifyTokenMustBeInCommandPosition(t *testing.T) {
	// The exact probe scenario from the issue: previously returned true and
	// silenced the detector for the whole run.
	bareArgCmds := []string{
		"grep -n test main.go",     // THE probe scenario
		"grep -rn build .",         // "build" as search pattern
		"cat lint_config.yaml",     // exact token at arg position
		"ls -la verify results/",   // multiple arg-position verbs
		"git diff check",           // branch named check
		"cp test /tmp/",            // file named test
		"echo vet compile check",   // all args, no command verb
		"tail -f build.log",        // filename arg
		"rm -rf test build lint",   // filename args
		"grep --include=check-* .", // flag value
		"find . -name test_*.go",   // pattern arg
		"sort verify.txt | uniq",   // arg in first segment, command in second
		"go run lint_script.go",    // script filename after runner — not a verb
		"cargo run build_tool.rs",  // ditto: build_ prefix at non-first position
	}
	for _, c := range bareArgCmds {
		if psIsVerifyCommand(c) {
			t.Errorf("psIsVerifyCommand(%q) = true — argument-position token must not arm everVerified (#553)", c)
		}
	}

	// Real verification commands must keep counting, including new
	// command-position forms the old tokens[0]-only rule would have missed.
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
		"test",                    // bare verify verb as the command
		"check-all",               // #483 command-position hyphen variant
		"cd pkg && go test ./...", // command position after list separator
		"go build ./... && go vet ./...",
		"cd pkg; make test",
		"echo setup | grep -v test && go test ./...", // pipeline + list
		"uv run pytest", // runner-prefix subcommand
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

// TestIssue553_C_E2E_GrepFilenameDoesNotArmEverVerified: end-to-end guard —
// after `grep -n test main.go`, an unverified success claim must still fire.
func TestIssue553_C_E2E_GrepFilenameDoesNotArmEverVerified(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false)
	// Probe command: "test" is grep's filename argument, not verification.
	s.recordToolCall("run_command", map[string]interface{}{"command": "grep -n test main.go"}, false)

	if s.everVerified {
		t.Fatal("grep with 'test' filename argument must NOT set everVerified")
	}
	if hint := s.checkSuccessClaim("All tests pass."); hint == "" {
		t.Fatal("detector was silenced by filename-argument false positive (#553)")
	}
}

// TestIssue553_C_CommandPositionTokens: unit-test the position classifier.
func TestIssue553_C_CommandPositionTokens(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string // tokens that MUST be classified as command position
		bad  []string // tokens that MUST NOT be
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

// ---------------------------------------------------------------------------
// Bug A1: guidance hints must not pollute token metering.
// ---------------------------------------------------------------------------

// TestIssue553_A1_HintTextNotMetered: recordToolResultLen with the original
// length must estimate from the original, not the hint-polluted content.
func TestIssue553_A1_HintTextNotMetered(t *testing.T) {
	s := newTokenWasteBudgetState()

	original := "x" // 1 byte -> 1 est token (the probe's original 1-token result)
	polluted := original + "\n\n[some-guidance] " + strings.Repeat("hint text ", 8)

	// Polluted metering (old behavior): big token count.
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

// TestIssue553_A1_LegacyRecordStillMetersFullContent: the legacy entry point
// (no override) is unchanged — full content is metered.
func TestIssue553_A1_LegacyRecordStillMetersFullContent(t *testing.T) {
	s := newTokenWasteBudgetState()
	content := strings.Repeat("z", 400)
	s.recordToolResult("read_file", content, false, false, nil)
	if s.entries[0].tokens != estimateTokens(content) {
		t.Errorf("legacy recordToolResult metered %d, want %d", s.entries[0].tokens, estimateTokens(content))
	}
}

// TestIssue553_A1_OverrideNegativeLenFallsBack: originalLen < 0 means
// "use len(content)".
func TestIssue553_A1_OverrideNegativeLenFallsBack(t *testing.T) {
	content := strings.Repeat("y", 300)
	s := newTokenWasteBudgetState()
	s.recordToolResultLen("grep", content, -1, false, false, nil)
	if s.entries[0].tokens != estimateTokens(content) {
		t.Errorf("override -1 should fall back to len(content): got %d, want %d", s.entries[0].tokens, estimateTokens(content))
	}
}

// ---------------------------------------------------------------------------
// Bug A2: isNegativeResult markers must respect word boundaries.
// ---------------------------------------------------------------------------

// TestIssue553_A2_NegativeMarkersWordBoundary: "unclean working tree" is NOT
// a negative "clean" result (dirty-tree state info is valuable, but it is not
// the exempt clean-tree marker). Same for other stem collisions.
func TestIssue553_A2_NegativeMarkersWordBoundary(t *testing.T) {
	notNegative := []string{
		"unclean working tree",    // THE probe scenario
		"nothing noteworthy",      // "nothing to" stem collision? no: "notew..." — guard anyway
		"found 3 unclean files",   // mid-word "clean"
		"rechecked nothing found", // sanity: still negative via exact marker
	}
	// Only the first three must be non-negative.
	for _, c := range notNegative[:3] {
		if isNegativeResult(c) {
			t.Errorf("isNegativeResult(%q) = true — marker must match at word start (#553 A2)", c)
		}
	}
	if !isNegativeResult(notNegative[3]) {
		t.Errorf("isNegativeResult(%q) = false — exact marker must still match", notNegative[3])
	}

	// Genuine negative results keep their exemption (#419 behavior intact).
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

// TestIssue553_A2_UncleanShortResultCountsAsEmpty: end-to-end — a short
// "unclean working tree" result is now classified wasteEmpty (not exempt),
// so it counts toward the waste ratio.
func TestIssue553_A2_UncleanShortResultCountsAsEmpty(t *testing.T) {
	s := newTokenWasteBudgetState()
	before := s.catTotals[wasteEmpty]
	s.recordToolResult("git_status", "unclean working tree", false, false, nil)
	if s.catTotals[wasteEmpty] == before {
		t.Error("'unclean working tree' must no longer be exempt from wasteEmpty (#553 A2)")
	}
}

// ---------------------------------------------------------------------------
// Bug A3: read-expiration keys must be path-normalized.
// ---------------------------------------------------------------------------

// TestIssue553_A3_AbsoluteReadRelativeEditExpires: absolute-path read +
// relative-path edit must still expire the read.
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
	// Relative edit — previously a silent key mismatch.
	s.markFileEdited(relPath)
	if s.wasteTokens == 0 {
		t.Error("relative-path edit must expire the absolute-path read (#553 A3)")
	}
	if s.catTotals[wasteExpired] == 0 {
		t.Error("expected wasteExpired tokens after cross-format edit")
	}
}

// TestIssue553_A3_RelativeReadAbsoluteEditExpires: the reverse direction.
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

// TestIssue553_A3_RedundantSlashAndDotNormalized: redundant separators and
// dot segments must not defeat key matching either.
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

// TestIssue553_A3_DifferentFilesStillDistinct: normalization must not
// over-match — edits to another file must not expire unrelated reads.
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

// TestIssue553_A3_WastePathKeyUnits: key function invariants.
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
