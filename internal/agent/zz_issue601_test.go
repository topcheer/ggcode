package agent

// Issue #601 tests — five defects in the write-integrity pipeline:
//
//	W1  computeFileChange's edit_file simulation diverged from the real
//	    tool (no replace_all field, no line-number-anchor resolution), so
//	    dry-run validation, diff preview, and checkpoint snapshots saw a
//	    different result than the actual write.
//	W2  checkHardcodedSecrets ran twice per write (registry + direct call
//	    in agent_tool.go), duplicating every secret warning.
//	W3  formatWarnings' cap truncated by registration index, not severity:
//	    an early advisory could crowd out a late sql-injection finding.
//	W4  five structural checks never compared OldContent, re-reporting
//	    pre-existing problems on every save (old==new triggered 5/5).
//	W5  checkEditBlastRadius divided by oldLines alone, counting a pure
//	    append as a >100% rewrite.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// ---------------------------------------------------------------------------
// W1: simulation == real edit_file (double-sided diff)
// ---------------------------------------------------------------------------

// runRealEditFile executes the REAL edit_file tool against a temp file and
// returns the resulting on-disk content (or the error result content).
func runRealEditFile(t *testing.T, dir, initialContent string, args map[string]any) (newContent string, isErr bool) {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("f%d.txt", len(dir)+len(initialContent)+len(fmt.Sprint(args))))
	if err := os.WriteFile(path, []byte(initialContent), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	raw, _ := json.Marshal(args)
	rawMap := map[string]any{}
	_ = json.Unmarshal(raw, &rawMap)
	rawMap["file_path"] = path
	raw, _ = json.Marshal(rawMap)

	ef := tool.EditFile{WorkingDir: dir, SandboxCheck: func(string) bool { return true }}
	res, err := ef.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("real edit_file execute: %v", err)
	}
	if res.IsError {
		return "", true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return string(data), false
}

// TestIssue601_W1_SimMatchesRealTool runs the same inputs through the real
// edit_file tool and the computeFileChange simulation (via simulateEditFile)
// and asserts byte-identical outcomes, including error agreement.
func TestIssue601_W1_SimMatchesRealTool(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name           string
		content        string
		oldText        string
		newText        string
		replaceAll     bool
		skipRealNoopOK bool // real tool reports "No change" result; content unchanged
	}{
		{
			name:       "replace_all replaces every occurrence",
			content:    "alpha\nfoo\nbeta\nfoo\ngamma\n",
			oldText:    "foo",
			newText:    "bar",
			replaceAll: true,
		},
		{
			name:    "replace_all=false replaces first only",
			content: "alpha\nfoo\nbeta\nfoo\ngamma\n",
			oldText: "foo",
			newText: "bar",
		},
		{
			name:    "line-number anchored old_text resolves",
			content: "alpha\nbeta\ngamma\n",
			oldText: "2\tbeta",
			newText: "2\tBETA",
		},
		{
			name:    "multi-line numbered block anchor",
			content: "one\ntwo\nthree\nfour\n",
			oldText: "2\ttwo\n3\tthree",
			newText: "2\tTWO\n3\tTHREE",
		},
		{
			name:    "non-unique without replace_all errors",
			content: "a\nfoo\nb\nfoo\nc\n",
			oldText: "foo",
			newText: "x",
		},
		{
			name:    "not-found errors",
			content: "alpha\nbeta\n",
			oldText: "nope",
			newText: "x",
		},
		{
			name:    "unique exact match",
			content: "package x\n\nfunc a() {}\n",
			oldText: "func a() {}",
			newText: "func b() {}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{
				"old_text": tc.oldText,
				"new_text": tc.newText,
			}
			if tc.replaceAll {
				args["replace_all"] = true
			}
			realNew, realErr := runRealEditFile(t, dir, tc.content, args)
			simNew, simErr := simulateEditFile(tc.content, tc.oldText, tc.newText, tc.replaceAll)

			if realErr != (simErr != nil) {
				t.Fatalf("error divergence: real isErr=%v, sim err=%v", realErr, simErr)
			}
			if !realErr && realNew != simNew {
				t.Fatalf("content divergence:\nreal: %q\nsim:  %q", realNew, simNew)
			}
		})
	}
}

// TestIssue601_W1_ReplaceAllJSONFieldParses pins the (a) half of W1: the
// simulation's args struct previously had no replace_all field, so
// json.Unmarshal silently dropped it.
func TestIssue601_W1_ReplaceAllJSONFieldParses(t *testing.T) {
	content := "x\nTOKEN\ny\nTOKEN\nz\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "kv.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	tc := provider.ToolCallDelta{
		Name: "edit_file",
		Arguments: json.RawMessage(`{
			"file_path": ` + jsonQuote(path) + `,
			"old_text": "TOKEN",
			"new_text": "OK",
			"replace_all": true
		}`),
	}
	_, _, newContent, _, err := a.computeFileChange(tc)
	if err != nil {
		t.Fatalf("computeFileChange: %v", err)
	}
	if got := strings.Count(newContent, "OK"); got != 2 {
		t.Fatalf("replace_all lost: expected 2 replacements, got %d in %q", got, newContent)
	}
	if strings.Contains(newContent, "TOKEN") {
		t.Fatalf("replace_all not applied: %q", newContent)
	}
}

// TestIssue601_W1_LineAnchorResolves pins the (b) half of W1: a
// line-number-anchored old_text ("N\tcontent") previously failed the
// literal Replace and silently produced a no-op simulation.
func TestIssue601_W1_LineAnchorResolves(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "anchor.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	tc := provider.ToolCallDelta{
		Name: "edit_file",
		Arguments: json.RawMessage(`{
			"file_path": ` + jsonQuote(path) + `,
			"old_text": "2\tbeta",
			"new_text": "2\tBETA"
		}`),
	}
	_, oldContent, newContent, _, err := a.computeFileChange(tc)
	if err != nil {
		t.Fatalf("computeFileChange: %v", err)
	}
	want := "alpha\nBETA\ngamma\n"
	if newContent != want {
		t.Fatalf("anchored edit mismatch:\nold: %q\ngot:  %q\nwant: %q", oldContent, newContent, want)
	}
	if newContent == oldContent {
		t.Fatal("line-number anchored edit was a silent no-op (pre-W1 bug)")
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---------------------------------------------------------------------------
// W2: hardcoded-secret warnings surface exactly once
// ---------------------------------------------------------------------------

// TestIssue601_W2_SingleSecretWarning verifies the registry is the single
// source of truth: one introduced AWS key produces exactly one warning
// (previously the direct call in agent_tool.go appended a duplicate copy).
func TestIssue601_W2_SingleSecretWarning(t *testing.T) {
	awsKey := "AKIA" + strings.Repeat("A", 16)
	old := "region: us-east-1\n"
	newC := "region: us-east-1\naws_access_key_id: " + awsKey + "\n"

	result := checkWriteIntegrity("config.txt", old, newC)
	if result == "" {
		t.Fatal("expected hardcoded-secret warning")
	}
	n := strings.Count(result, "aws_access_key")
	if n != 1 {
		t.Fatalf("expected exactly 1 aws_access_key warning, got %d:\n%s", n, result)
	}
	if strings.Count(result, "[Post-write integrity check]") != 1 {
		t.Fatalf("expected a single integrity block, got:\n%s", result)
	}
}

// ---------------------------------------------------------------------------
// W3: severity-ranked cap
// ---------------------------------------------------------------------------

// TestIssue601_W3_SeverityBeatsRegistrationOrder builds a change that
// triggers BOTH an early-registered advisory (edit-blast-radius, severity
// default) and a late-registered critical (sql-injection). With
// maxIntegrityWarnings == 1 the critical one must surface.
func TestIssue601_W3_SeverityBeatsRegistrationOrder(t *testing.T) {
	// 25-line old file (>= 20 so blast-radius is armed).
	var oldB strings.Builder
	oldB.WriteString("package main\n\nimport \"database/sql\"\n\nfunc old1() { _ = sql.Done }\n")
	for i := 2; countLines(oldB.String()) < 25; i++ {
		oldB.WriteString(fmt.Sprintf("func old%d() { _ = sql.Done%d }\n", i, i))
	}
	oldContent := oldB.String()
	if countLines(oldContent) < 20 {
		t.Fatalf("test setup: need >=20 old lines, got %d", countLines(oldContent))
	}

	// New content: fully rewritten (blast-radius >= 60%) AND carrying a SQL
	// injection pattern (critical, registered far later than blast-radius).
	var nb strings.Builder
	nb.WriteString("package main\n\nimport (\n\t\"database/sql\"\n)\n\nfunc q(db *sql.DB, name string) {\n\tdb.Query(\"SELECT * FROM users WHERE name = '\" + name + \"'\")\n}\n")
	for i := 0; countLines(nb.String()) < 30; i++ {
		nb.WriteString(fmt.Sprintf("// filler line %d for rewrite parity\n", i))
	}
	newContent := nb.String()

	// Sanity: both checks fire individually.
	blast := checkEditBlastRadius("main.go", oldContent, newContent)
	if blast == "" {
		t.Fatal("test setup: expected edit-blast-radius to fire on full rewrite")
	}
	sqlw := checkSQLInjection("main.go", oldContent, newContent)
	if len(sqlw) == 0 {
		t.Fatal("test setup: expected sql-injection to fire")
	}

	// Registry pipeline: the cap must keep the CRITICAL warning.
	result := checkWriteIntegrity("main.go", oldContent, newContent)
	if result == "" {
		t.Fatal("expected a warning")
	}
	if !strings.Contains(strings.ToLower(result), "sql") {
		t.Fatalf("expected sql-injection (critical) to survive the cap, got:\n%s", result)
	}
	if strings.Contains(strings.ToLower(result), "blast-radius") {
		t.Fatalf("advisory edit-blast-radius must lose to critical sql-injection:\n%s", result)
	}
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }

// ---------------------------------------------------------------------------
// W4: zero-delta / pre-existing problems are not re-reported
// ---------------------------------------------------------------------------

// TestIssue601_W4_ZeroDeltaNoWarnings: old == new with pre-existing
// problems (conflict markers, unbalanced delimiters, null bytes) must
// produce no warnings — the write introduced nothing.
func TestIssue601_W4_ZeroDeltaNoWarnings(t *testing.T) {
	bad := "intro\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> dev\nunbalanced (\n\x00\n"
	if w := checkWriteIntegrity("doc.md", bad, bad); w != "" {
		t.Fatalf("zero-delta write must not warn, got:\n%s", w)
	}
}

// TestIssue601_W4_PreExistingProblemNotReReported: a write that appends a
// clean line to a file that already had problems must not re-report them.
func TestIssue601_W4_PreExistingProblemNotReReported(t *testing.T) {
	oldC := "# doc\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> dev\n"
	newC := oldC + "clean appended line\n"
	if w := checkWriteIntegrity("doc.md", oldC, newC); w != "" {
		t.Fatalf("pre-existing conflict markers must not be re-reported, got:\n%s", w)
	}
}

// TestIssue601_W4_IntroducedProblemStillReported: the gate must not swallow
// NEW problems — a write that introduces conflict markers is flagged.
func TestIssue601_W4_IntroducedProblemStillReported(t *testing.T) {
	oldC := "# doc\nintro\n"
	newC := "# doc\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> dev\n"
	w := checkWriteIntegrity("doc.md", oldC, newC)
	if w == "" || !strings.Contains(w, "merge conflict") {
		t.Fatalf("newly introduced conflict markers must be reported, got:\n%s", w)
	}
}

// TestIssue601_W4_BinaryCorruptionDeltaGated: null bytes are only reported
// when this write increases their count.
func TestIssue601_W4_BinaryCorruptionDeltaGated(t *testing.T) {
	oldC := "a\x00b\nc\n"
	touch := "a\x00b\nc\nd\n" // same null count, one clean line appended
	if w := checkWriteIntegrity("data.bin", oldC, touch); w != "" {
		t.Fatalf("unchanged null-byte count must not warn, got:\n%s", w)
	}
	worse := "a\x00b\x00c\n"
	w := checkWriteIntegrity("data.bin", oldC, worse)
	if w == "" || !strings.Contains(w, "null byte") {
		t.Fatalf("introduced null bytes must warn, got:\n%s", w)
	}
}

// ---------------------------------------------------------------------------
// W5: pure append is not a >100% rewrite
// ---------------------------------------------------------------------------

func linesStr(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// TestIssue601_W5_PureAppendNotRewrite: appending 25 lines to a 20-line
// file previously computed 125%; it must now stay <= 100% and (for the
// probe's 15-line case) not trip the 60% accidental-rewrite gate.
func TestIssue601_W5_PureAppendNotRewrite(t *testing.T) {
	oldC := linesStr(20)

	// 15-line append: 15/35 = 43% -> no warning (probe case was 75% -> FP).
	if w := checkWriteIntegrity("notes.md", oldC, oldC+linesStr(15)); w != "" {
		t.Fatalf("15-line append must not be flagged as rewrite, got:\n%s", w)
	}

	// 25-line append: 25/45 = 56% -> no warning, and never "125%".
	w := checkWriteIntegrity("notes.md", oldC, oldC+linesStr(25))
	if w != "" && strings.Contains(w, "125%") {
		t.Fatalf("pure append must not exceed 100%%: %s", w)
	}

	// A true full rewrite still trips the gate: 20 changed / max(20,20) = 100%.
	var rb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&rb, "rewritten %d\n", i)
	}
	if w := checkWriteIntegrity("notes.md", oldC, rb.String()); w == "" {
		t.Fatal("full rewrite must still be flagged")
	}
}

// TestIssue601_W5_AppendNeverExceeds100Percent: direct unit probe of the
// denominator — appending 30 lines to a 20-line file yields 30/50 = 60%,
// and the percentage reported (if any) can never exceed 100.
func TestIssue601_W5_AppendNeverExceeds100Percent(t *testing.T) {
	oldC := linesStr(20)
	newC := oldC + linesStr(80) // 80/100 = 80%: legitimately warns, but <= 100%.
	w := checkEditBlastRadius("notes.md", oldC, newC)
	if w == "" {
		t.Fatal("80% append+ context: expected blast-radius warning for a near-total addition")
	}
	if p := extractPercent(w); p > 100 {
		t.Fatalf("percentage must never exceed 100 for an append, got %d: %s", p, w)
	}
}

func extractPercent(s string) int {
	i := strings.Index(s, "%")
	if i < 0 {
		return 0
	}
	j := i
	for j > 0 && s[j-1] >= '0' && s[j-1] <= '9' {
		j--
	}
	n := 0
	for _, c := range s[j:i] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
