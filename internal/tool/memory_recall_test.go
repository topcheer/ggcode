package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

// mkRecallStores builds isolated global+project memory stores with fixture
// entries used by the memory_recall tests.
func mkRecallStores(t *testing.T) (global, project *memory.AutoMemory) {
	t.Helper()
	withTestHome(t)
	global = memory.NewAutoMemory()
	if err := global.SaveMemory("release-checklist", "Tag releases with annotated git tags before pushing"); err != nil {
		t.Fatal(err)
	}
	project = memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("build-process", "# Build\nUse make verify-ci before pushing\n"); err != nil {
		t.Fatal(err)
	}
	if err := project.SaveMemory("deploy-notes", "Random notes without the magic word"); err != nil {
		t.Fatal(err)
	}
	return global, project
}

func TestMemoryRecall_CrossScopeSearch(t *testing.T) {
	global, project := mkRecallStores(t)
	tol := NewMemoryRecallTool(global, project)

	out, err := tol.Execute(context.Background(), json.RawMessage(`{"query":"pushing"}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v / %+v", err, out)
	}
	for _, want := range []string{"[project] build-process", "[global] release-checklist", "make verify-ci"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("output missing %q:\n%s", want, out.Content)
		}
	}
}

func TestMemoryRecall_ScopeFilter(t *testing.T) {
	global, project := mkRecallStores(t)
	tol := NewMemoryRecallTool(global, project)

	out, err := tol.Execute(context.Background(), json.RawMessage(`{"query":"make verify-ci","scope":"project"}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v / %+v", err, out)
	}
	if !strings.Contains(out.Content, "build-process") {
		t.Fatalf("project match missing:\n%s", out.Content)
	}
	if strings.Contains(out.Content, "release-checklist") {
		t.Fatalf("global entry leaked into project-scoped search:\n%s", out.Content)
	}

	out, err = tol.Execute(context.Background(), json.RawMessage(`{"query":"release","scope":"global"}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v / %+v", err, out)
	}
	if !strings.Contains(out.Content, "release-checklist") {
		t.Fatalf("global match missing:\n%s", out.Content)
	}
	if strings.Contains(out.Content, "build-process") {
		t.Fatalf("project entry leaked into global-scoped search:\n%s", out.Content)
	}
}

func TestMemoryRecall_ScoreOrderTitleBeatsBody(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("deploy-runbook", "canonical deploy steps live here"); err != nil {
		t.Fatal(err)
	}
	if err := project.SaveMemory("misc-notes", "also mentions deploy in passing"); err != nil {
		t.Fatal(err)
	}
	tol := NewMemoryRecallTool(nil, project)

	out, err := tol.Execute(context.Background(), json.RawMessage(`{"query":"deploy"}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v / %+v", err, out)
	}
	runbookIdx := strings.Index(out.Content, "deploy-runbook")
	notesIdx := strings.Index(out.Content, "misc-notes")
	if runbookIdx == -1 || notesIdx == -1 {
		t.Fatalf("expected both matches:\n%s", out.Content)
	}
	if runbookIdx > notesIdx {
		t.Fatalf("title hit (deploy-runbook, 3pts) should rank above body hit (misc-notes, 1pt):\n%s", out.Content)
	}
}

func TestMemoryRecall_Limit(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	for _, key := range []string{"build-a", "build-b", "build-c"} {
		if err := project.SaveMemory(key, "content about builds"); err != nil {
			t.Fatal(err)
		}
	}
	tol := NewMemoryRecallTool(nil, project)

	out, err := tol.Execute(context.Background(), json.RawMessage(`{"query":"build","limit":2}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v / %+v", err, out)
	}
	if got := strings.Count(out.Content, "updated "); got != 2 {
		t.Fatalf("expected exactly 2 results with limit 2, got %d:\n%s", got, out.Content)
	}
}

func TestMemoryRecall_NoMatchIsResultNotError(t *testing.T) {
	global, project := mkRecallStores(t)
	tol := NewMemoryRecallTool(global, project)

	out, err := tol.Execute(context.Background(), json.RawMessage(`{"query":"kubernetes-helm-chart"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.IsError {
		t.Fatalf("no-match must be a normal result, not an error: %+v", out)
	}
	if !strings.Contains(out.Content, "no matches") {
		t.Fatalf("expected explicit no-matches marker:\n%s", out.Content)
	}
}

func TestMemoryRecall_ParamValidation(t *testing.T) {
	global, project := mkRecallStores(t)
	tol := NewMemoryRecallTool(global, project)

	out, err := tol.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !out.IsError {
		t.Fatalf("missing query must be an in-band error: %v / %+v", err, out)
	}
	if !strings.Contains(out.Content, "query is required") {
		t.Fatalf("unexpected message: %s", out.Content)
	}

	out, err = tol.Execute(context.Background(), json.RawMessage(`{"query":"build","scope":"galaxy"}`))
	if err != nil || !out.IsError {
		t.Fatalf("invalid scope must be an in-band error: %v / %+v", err, out)
	}
}

func TestMemoryRecall_NilStoresDegradeToNoMatches(t *testing.T) {
	tol := NewMemoryRecallTool(nil, nil)
	out, err := tol.Execute(context.Background(), json.RawMessage(`{"query":"anything"}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v / %+v", err, out)
	}
	if !strings.Contains(out.Content, "no matches") {
		t.Fatalf("expected no-matches result:\n%s", out.Content)
	}
}
