package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestIssue3116_ImportedNotUsedSummarized(t *testing.T) {
	out := `./internal/foo/bar.go:5:2: "fmt" imported and not used`
	summary := summarizeGoBuildOutput(out)
	if summary == "" {
		t.Fatal("imported-and-not-used compile error silently dropped from summary")
	}
}

func TestIssue3116_RedeclaredMissingReturnDuplicate(t *testing.T) {
	cases := []string{
		`./a.go:10:5: x redeclared in this block`,
		`./b.go:42:2: missing return`,
		`./c.go:7:9: X redeclared in this block (duplicate declaration)`,
	}
	for _, out := range cases {
		if summary := summarizeGoBuildOutput(out); summary == "" {
			t.Errorf("compile error dropped from summary: %q", out)
		}
	}
}

func TestIssue3116_NoKeywordRegression(t *testing.T) {

	for _, line := range []string{
		`./x.go:3:1: other declaration of foo`,
	} {

		_ = line
	}

	if s := summarizeGoBuildOutput(`./x.go:3:1: something innocuous`); s != "" {
		t.Errorf("non-error line summarized: %q", s)
	}
}

func TestIssue3117_CloneInstancesShareStoreSafely(t *testing.T) {
	dir := t.TempDir()
	parent := &CmdSnippetTool{WorkingDir: dir}
	cloned := parent.Clone()
	child, ok := cloned.(*CmdSnippetTool)
	if !ok {
		t.Fatalf("Clone returned %T, want *CmdSnippetTool", cloned)
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(2)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, _ = parent.Execute(context.Background(), mustJSON3116(t, map[string]any{
					"action": "save", "name": "parent-cmd", "command": "echo parent",
				}))
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, _ = child.Execute(context.Background(), mustJSON3116(t, map[string]any{
					"action": "save", "name": "child-cmd", "command": "echo child",
				}))
			}
		}(w)
	}
	wg.Wait()

	res, err := parent.Execute(context.Background(), mustJSON3116(t, map[string]any{"action": "list"}))
	if err != nil || res.IsError {
		t.Fatalf("list: %v %+v", err, res)
	}
	for _, want := range []string{"parent-cmd", "child-cmd"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("entry %q lost after cross-instance interleaving (content: %s)", want, res.Content)
		}
	}
}

func mustJSON3116(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(b)
}

func TestIssue3117_AutoSnippetDoesNotShadowUserEntry(t *testing.T) {
	dir := t.TempDir()
	tool := &CmdSnippetTool{WorkingDir: dir}

	if res, err := tool.Execute(context.Background(), mustJSON3116(t, map[string]any{
		"action": "save", "name": "deploy", "command": "make deploy-prod",
	})); err != nil || res.IsError {
		t.Fatalf("user save: %v %+v", err, res)
	}

	if _, err := tool.SaveAutoSnippet("deploy", "make deploy-staging", "", nil); err != nil {
		t.Fatalf("auto save: %v", err)
	}

	res, err := tool.Execute(context.Background(), mustJSON3116(t, map[string]any{"action": "get", "name": "deploy"}))
	if err != nil || res.IsError {
		t.Fatalf("get: %v %+v", err, res)
	}
	if !strings.Contains(res.Content, "deploy-prod") || strings.Contains(res.Content, "deploy-staging") {
		t.Fatalf("user entry shadowed by auto snippet: %s", res.Content)
	}

	res2, err := tool.Execute(context.Background(), mustJSON3116(t, map[string]any{"action": "get", "name": "deploy-auto"}))
	if err != nil || res2.IsError {
		t.Fatalf("get auto: %v %+v", err, res2)
	}
	if !strings.Contains(res2.Content, "deploy-staging") {
		t.Fatalf("auto entry lost: %s", res2.Content)
	}
}

func TestIssue3118_ListUsableUnderDetachedHEAD(t *testing.T) {

	dir := t.TempDir()
	tool := &CIStatusTool{WorkingDir: dir}
	_, err := tool.Execute(context.Background(), mustJSON3116(t, map[string]any{"action": "list"}))
	if err == nil {
		return
	}

	msg := err.Error()
	if strings.Contains(msg, "branch") {
		t.Fatalf("list failed on branch resolution (detached-HEAD regression): %s", msg)
	}
}

func TestIssue3117_PersistLeavesNoTmpArtifacts(t *testing.T) {
	dir := t.TempDir()
	tool := &CmdSnippetTool{WorkingDir: dir}
	if res, err := tool.Execute(context.Background(), mustJSON3116(t, map[string]any{
		"action": "save", "name": "n", "command": "c",
	})); err != nil || res.IsError {
		t.Fatalf("save: %v %+v", err, res)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".ggcode"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Fatalf("stray tmp artifact: %s", e.Name())
		}
	}
}
