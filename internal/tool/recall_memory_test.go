package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// newRecallTestTool builds a RecallMemoryTool pointed at a temp session dir
// containing one known session.
func newRecallTestTool(t *testing.T) *RecallMemoryTool {
	t.Helper()
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := session.NewSession("zai", "ep", "glm")
	ses.ID = "test-session-1"
	ses.Title = "Fix flaky CI test"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "The e2e test hangs because the browser process never exits."}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Root cause: the browser leak guard did not close the CDP connection on context cancellation."}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}
	return &RecallMemoryTool{Dir: dir}
}

func rawInput(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRecallMemoryFindsPastSession(t *testing.T) {
	tool := newRecallTestTool(t)
	res, err := tool.Execute(context.Background(), rawInput(t, map[string]any{
		"query": "flaky e2e browser hang",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "flaky CI test") {
		t.Fatalf("output missing session title: %s", res.Content)
	}
	if !strings.Contains(res.Content, "test-session-1") {
		t.Fatalf("output missing session id: %s", res.Content)
	}
	if !strings.Contains(res.Content, "score") {
		t.Fatalf("output missing score signal: %s", res.Content)
	}
}

func TestRecallMemoryRoleAndTimeFilters(t *testing.T) {
	tool := newRecallTestTool(t)

	res, err := tool.Execute(context.Background(), rawInput(t, map[string]any{
		"query": "browser leak guard",
		"role":  "user",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	// "browser leak guard" only appears in the assistant message; with the
	// user-role filter the result must be empty (no user message matches).
	if !strings.Contains(res.Content, "No past-session matches") {
		t.Fatalf("expected empty result under user filter, got: %s", res.Content)
	}
}

func TestRecallMemoryValidation(t *testing.T) {
	tool := newRecallTestTool(t)

	res, err := tool.Execute(context.Background(), rawInput(t, map[string]any{"query": "   "}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("blank query must be an error result")
	}

	res, err = tool.Execute(context.Background(), rawInput(t, map[string]any{"query": "x", "role": "system"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("invalid role must be an error result")
	}
}

func TestRecallMemoryMaxResultsClamp(t *testing.T) {
	tool := newRecallTestTool(t)
	res, err := tool.Execute(context.Background(), rawInput(t, map[string]any{
		"query":       "browser",
		"max_results": 9999,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
}

func TestRecallMemoryWorkspaceScopedByDefault(t *testing.T) {
	// The test process runs in the ggcode worktree; the fixture session has
	// no workspace marker so the default (current-workspace) scope includes
	// legacy empty-workspace sessions — verify all_workspaces=false still
	// finds it, proving legacy records stay recallable.
	tool := newRecallTestTool(t)
	res, err := tool.Execute(context.Background(), rawInput(t, map[string]any{
		"query": "CDP connection",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if strings.Contains(res.Content, "No past-session matches") {
		t.Fatalf("legacy empty-workspace session should be recallable in workspace scope: %s", res.Content)
	}
}

func TestRecallMemoryEmptyDir(t *testing.T) {
	tool := &RecallMemoryTool{Dir: t.TempDir(), Now: time.Now()}
	res, err := tool.Execute(context.Background(), rawInput(t, map[string]any{"query": "anything"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("empty store must not error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No past-session matches") {
		t.Fatalf("expected empty-result guidance, got: %s", res.Content)
	}
}

func TestRecallMemoryDefaultDirResolve(t *testing.T) {
	// Dir unset → DefaultDir() path. Point HOME at a temp dir so the test
	// never touches the developer's real ~/.ggcode.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if _, err := os.Stat(filepath.Join(dir, ".ggcode")); err == nil {
		t.Fatal("unexpected pre-existing .ggcode in temp HOME")
	}
	tool := &RecallMemoryTool{}
	if _, err := tool.Execute(context.Background(), rawInput(t, map[string]any{"query": "zzz-nonexistent"})); err != nil {
		t.Fatalf("DefaultDir resolution failed: %v", err)
	}
}
