package agent

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

func TestIssue601_W1_SimMatchesRealTool(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name           string
		content        string
		oldText        string
		newText        string
		replaceAll     bool
		skipRealNoopOK bool
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

func TestIssue601_W3_SeverityBeatsRegistrationOrder(t *testing.T) {
	var oldB strings.Builder
	oldB.WriteString("package main\n\nimport \"database/sql\"\n\nfunc old1() { _ = sql.Done }\n")
	for i := 2; countLines(oldB.String()) < 25; i++ {
		oldB.WriteString(fmt.Sprintf("func old%d() { _ = sql.Done%d }\n", i, i))
	}
	oldContent := oldB.String()
	if countLines(oldContent) < 20 {
		t.Fatalf("test setup: need >=20 old lines, got %d", countLines(oldContent))
	}

	var nb strings.Builder
	nb.WriteString("package main\n\nimport (\n\t\"database/sql\"\n)\n\nfunc q(db *sql.DB, name string) {\n\tdb.Query(\"SELECT * FROM users WHERE name = '\" + name + \"'\")\n}\n")
	for i := 0; countLines(nb.String()) < 30; i++ {
		nb.WriteString(fmt.Sprintf("// filler line %d for rewrite parity\n", i))
	}
	newContent := nb.String()

	blast := checkEditBlastRadius("main.go", oldContent, newContent)
	if blast == "" {
		t.Fatal("test setup: expected edit-blast-radius to fire on full rewrite")
	}
	sqlw := checkSQLInjection("main.go", oldContent, newContent)
	if len(sqlw) == 0 {
		t.Fatal("test setup: expected sql-injection to fire")
	}

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

func TestIssue601_W4_ZeroDeltaNoWarnings(t *testing.T) {
	bad := "intro\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> dev\nunbalanced (\n\x00\n"
	if w := checkWriteIntegrity("doc.md", bad, bad); w != "" {
		t.Fatalf("zero-delta write must not warn, got:\n%s", w)
	}
}

func TestIssue601_W4_PreExistingProblemNotReReported(t *testing.T) {
	oldC := "# doc\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> dev\n"
	newC := oldC + "clean appended line\n"
	if w := checkWriteIntegrity("doc.md", oldC, newC); w != "" {
		t.Fatalf("pre-existing conflict markers must not be re-reported, got:\n%s", w)
	}
}

func TestIssue601_W4_IntroducedProblemStillReported(t *testing.T) {
	oldC := "# doc\nintro\n"
	newC := "# doc\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> dev\n"
	w := checkWriteIntegrity("doc.md", oldC, newC)
	if w == "" || !strings.Contains(w, "merge conflict") {
		t.Fatalf("newly introduced conflict markers must be reported, got:\n%s", w)
	}
}

func TestIssue601_W4_BinaryCorruptionDeltaGated(t *testing.T) {
	oldC := "a\x00b\nc\n"
	touch := "a\x00b\nc\nd\n"
	if w := checkWriteIntegrity("data.bin", oldC, touch); w != "" {
		t.Fatalf("unchanged null-byte count must not warn, got:\n%s", w)
	}
	worse := "a\x00b\x00c\n"
	w := checkWriteIntegrity("data.bin", oldC, worse)
	if w == "" || !strings.Contains(w, "null byte") {
		t.Fatalf("introduced null bytes must warn, got:\n%s", w)
	}
}

func linesStr(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestIssue601_W5_PureAppendNotRewrite(t *testing.T) {
	oldC := linesStr(20)

	if w := checkWriteIntegrity("notes.md", oldC, oldC+linesStr(15)); w != "" {
		t.Fatalf("15-line append must not be flagged as rewrite, got:\n%s", w)
	}

	w := checkWriteIntegrity("notes.md", oldC, oldC+linesStr(25))
	if w != "" && strings.Contains(w, "125%") {
		t.Fatalf("pure append must not exceed 100%%: %s", w)
	}

	var rb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&rb, "rewritten %d\n", i)
	}
	if w := checkWriteIntegrity("notes.md", oldC, rb.String()); w == "" {
		t.Fatal("full rewrite must still be flagged")
	}
}

func TestIssue601_W5_AppendNeverExceeds100Percent(t *testing.T) {
	oldC := linesStr(20)
	newC := oldC + linesStr(80)
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
