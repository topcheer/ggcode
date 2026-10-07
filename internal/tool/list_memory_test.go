package tool

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

func mkMemStores(t *testing.T) (global, project *memory.AutoMemory) {
	t.Helper()
	withTestHome(t)
	global = memory.NewAutoMemory()
	t.Cleanup(func() { _ = os.RemoveAll(global.Dir()) })
	if err := global.SaveMemory("global-key", "Always run gofmt after edits"); err != nil {
		t.Fatal(err)
	}
	project = memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("build-process", "# Build\nUse make verify-ci before pushing\n"); err != nil {
		t.Fatal(err)
	}
	return global, project
}

func TestListMemoryTool_DefaultAllScopes(t *testing.T) {
	global, project := mkMemStores(t)
	tol := NewListMemoryTool(global, project)

	out, err := tol.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: %v / %+v", err, out)
	}
	for _, want := range []string{"project (1 entries)", "global (1 entries)", "build-process", "Use make verify-ci", "global-key", "Always run gofmt"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("output missing %q:\n%s", want, out.Content)
		}
	}
}

func TestListMemoryTool_HeadingStrippedFromPreview(t *testing.T) {
	_, project := mkMemStores(t)
	tol := NewListMemoryTool(nil, project)

	out, _ := tol.Execute(context.Background(), json.RawMessage(`{"scope":"project"}`))
	if strings.Contains(out.Content, "== global") {
		t.Fatalf("project scope must not include global section:\n%s", out.Content)
	}
	// "# Build" heading must be stripped; first body line is the preview.
	if !strings.Contains(out.Content, "build-process: Use make verify-ci") {
		t.Fatalf("preview must skip markdown heading:\n%s", out.Content)
	}
}

func TestListMemoryTool_EmptyScopeNoMemories(t *testing.T) {
	withTestHome(t)
	tol := NewListMemoryTool(nil, memory.NewProjectAutoMemory(t.TempDir()))

	out, _ := tol.Execute(context.Background(), json.RawMessage(`{"scope":"project"}`))
	if !strings.Contains(out.Content, "No stored memories") {
		t.Fatalf("empty store must report clearly, got:\n%s", out.Content)
	}
}

func TestListMemoryTool_InvalidScope(t *testing.T) {
	tol := NewListMemoryTool(nil, nil)
	out, _ := tol.Execute(context.Background(), json.RawMessage(`{"scope":"bogus"}`))
	if !out.IsError {
		t.Fatalf("invalid scope must be an error, got:\n%s", out.Content)
	}
}

func TestListMemoryTool_NilScopeStoreSkipped(t *testing.T) {
	_, project := mkMemStores(t)
	// globalMem nil, scope all: project section still renders, no panic.
	tol := NewListMemoryTool(nil, project)
	out, _ := tol.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(out.Content, "build-process") {
		t.Fatalf("nil global store must not break listing:\n%s", out.Content)
	}
}

// r488 (Mem++ arXiv:2610.02002) tool-level coverage: key view shows archive
// depth, as_of reads the historical version, as_of without key is rejected.
func TestListMemoryTool_KeyViewShowsArchiveDepth(t *testing.T) {
	_, project := mkMemStores(t)
	if err := project.SaveMemory("build-process", "# Build v2\nmake verify-ci twice\n"); err != nil {
		t.Fatal(err) // second write archives v1 per r488
	}
	tol := NewListMemoryTool(nil, project)
	out, _ := tol.Execute(context.Background(), json.RawMessage(`{"key":"build-process"}`))
	if out.IsError {
		t.Fatalf("key view errored: %s", out.Content)
	}
	for _, want := range []string{"build-process", "1 archived version(s)", "make verify-ci twice"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("key view missing %q:\n%s", want, out.Content)
		}
	}
}

func TestListMemoryTool_AsOfRequiresKey(t *testing.T) {
	_, project := mkMemStores(t)
	tol := NewListMemoryTool(nil, project)
	out, _ := tol.Execute(context.Background(), json.RawMessage(`{"as_of":"2026-10-07T09:00:00Z"}`))
	if !out.IsError {
		t.Fatalf("as_of without key must be rejected, got:\n%s", out.Content)
	}
}

func TestListMemoryTool_AsOfReadsHistoricalVersion(t *testing.T) {
	_, project := mkMemStores(t) // build-process v1 written here
	// mid is strictly between the two writes: after v1, before v2.
	mid := time.Now()
	time.Sleep(5 * time.Millisecond)
	if err := project.SaveMemory("build-process", "v2 content"); err != nil {
		t.Fatal(err)
	}
	// mid was captured AFTER v1 but BEFORE v2's save.
	tol := NewListMemoryTool(nil, project)
	payload := `{"key":"build-process","as_of":"` + mid.Format(time.RFC3339Nano) + `"}`
	out, _ := tol.Execute(context.Background(), json.RawMessage(payload))
	if out.IsError {
		t.Fatalf("as_of read errored: %s", out.Content)
	}
	if !strings.Contains(out.Content, "Use make verify-ci before pushing") {
		t.Fatalf("as_of must return v1 content, got:\n%s", out.Content)
	}
	if strings.Contains(out.Content, "v2 content") {
		t.Fatalf("as_of must not leak live v2 content:\n%s", out.Content)
	}
}
