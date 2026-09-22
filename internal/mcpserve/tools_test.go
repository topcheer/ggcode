package mcpserve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// helperEnv switches the test binary into subprocess mode, simulating a
// headless ggcode pipe run (classic os/exec helper-process pattern).
const helperEnv = "GGCODE_MCP_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		runHelperProcess()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runHelperProcess emulates `ggcode -p <prompt> --output <path>`: it writes
// the final answer to the --output path and exits 0.
func runHelperProcess() {
	args := os.Args
	prompt := ""
	outPath := ""
	for i, a := range args {
		if a == "-p" && i+1 < len(args) {
			prompt = args[i+1]
		}
		if a == "--output" && i+1 < len(args) {
			outPath = args[i+1]
		}
	}
	_ = os.WriteFile(outPath, []byte("answer:"+prompt), 0o644)
}

// --- helpers shared with server_test.go ---

// writeRealSession persists one session with a user+assistant exchange via
// the real JSONL store so session-tool tests exercise the storage layer.
func writeRealSession(t *testing.T, dir, id, title, userText, asstText string) {
	t.Helper()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ses := session.NewSession("default", "default", "test-model")
	ses.ID = id
	ses.Title = title
	ses.Preview = userText
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save session: %v", err)
	}
	// AppendMessagesBatchToDisk both writes the messages and updates the
	// index (Save alone only touches the file, not the index).
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userText}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: asstText}}},
	}
	if err := store.AppendMessagesBatchToDisk(ses, msgs); err != nil {
		t.Fatalf("AppendMessagesBatchToDisk: %v", err)
	}
}

// newTempSessionSource returns a SessionSource backed by the given store dir.
func newTempSessionSource(dir string) *jsonlSessionSource {
	return &jsonlSessionSource{open: func() (*session.JSONLStore, error) {
		return session.NewJSONLStore(dir)
	}}
}

// firstContentText extracts the first text block of a tools/call result.
func firstContentText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in %v", result)
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] not an object: %v", content[0])
	}
	text, _ := block["text"].(string)
	return text
}

// isErrorResult reports whether the response carries isError=true.
func isErrorResult(t *testing.T, resp map[string]any) bool {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	isErr, _ := result["isError"].(bool)
	return isErr
}

// --- execRunner tests ---

func TestBuildRunArgs(t *testing.T) {
	got := buildRunArgs("do it", "/tmp/out.txt", "/cfg/ggcode.yaml")
	want := []string{"-p", "do it", "--output", "/tmp/out.txt", "--config", "/cfg/ggcode.yaml"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Empty config path must omit the flag entirely (child uses its own config).
	got = buildRunArgs("p", "o", "")
	if len(got) != 4 {
		t.Errorf("args without config = %v, want 4 elements", got)
	}
}

func TestNormalizeWorkingDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalizeWorkingDir("")
	if err != nil || got != cwd {
		t.Errorf("empty dir: got (%q,%v), want cwd", got, err)
	}
	got, err = normalizeWorkingDir(".")
	if err != nil || got != cwd {
		t.Errorf(". dir: got (%q,%v), want cwd", got, err)
	}
	if _, err := normalizeWorkingDir(filepath.Join(cwd, "definitely-missing-dir")); err == nil {
		t.Error("missing dir should error")
	}
}

func TestExecRunnerHelperProcess(t *testing.T) {
	r := newExecRunner("", "", 0)
	prompt := "say hi"
	r.execCommand = func(ctx context.Context, exe string, args []string, dir string, env []string) *exec.Cmd {
		if len(args) < 2 || args[0] != "-p" || args[1] != prompt {
			t.Errorf("child args = %v, want -p %q first", args, prompt)
		}
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), helperEnv+"=1")
		return cmd
	}
	res, err := r.Run(context.Background(), RunRequest{Prompt: prompt})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Output != "answer:say hi" {
		t.Errorf("output = %q", res.Output)
	}
}

func TestExecRunnerFailingProcess(t *testing.T) {
	r := newExecRunner("", "", 0)
	r.execCommand = func(ctx context.Context, exe string, args []string, dir string, env []string) *exec.Cmd {
		// Nonexistent binary → exec error with our stderr message shape.
		cmd := exec.CommandContext(ctx, filepath.Join(dir, "no-such-binary"))
		cmd.Dir = dir
		cmd.Stderr = nil // captured by Run itself
		return cmd
	}
	// The runner wraps cmd.Stderr AFTER execCommand returns, so give the cmd
	// a real buffer by not pre-setting Stderr; Run assigns it. A failing exec
	// must surface a wrapped error containing context.
	_, err := r.Run(context.Background(), RunRequest{Prompt: "x", WorkingDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for failing child")
	}
	if !strings.Contains(err.Error(), "ggcode run failed") {
		t.Errorf("error = %v, want wrapped 'ggcode run failed'", err)
	}
}

// --- transcript formatting ---

func TestFormatTranscript(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 3000)}}},
	}
	ses := &session.Session{ID: "s", Title: "T", Messages: msgs}
	out := formatTranscript(ses, 10)
	if !strings.Contains(out, "[1] user: hello") {
		t.Errorf("transcript missing user line: %q", out)
	}
	if !strings.Contains(out, "…[truncated]") {
		t.Errorf("long message should be truncated: %q", out)
	}
	// Tail window: only the last message when maxMessages=1.
	out = formatTranscript(ses, 1)
	if strings.Contains(out, "hello") {
		t.Errorf("tail window should drop old messages: %q", out)
	}
	// Nil session is safe.
	if out := formatTranscript(nil, 10); !strings.Contains(out, "empty") {
		t.Errorf("nil session: %q", out)
	}
}

func TestFormatSessionSummaries(t *testing.T) {
	if got := formatSessionSummaries(nil); !strings.Contains(got, "No sessions") {
		t.Errorf("empty list = %q", got)
	}
	got := formatSessionSummaries([]SessionSummary{{ID: "a", Title: "", Preview: "prev", UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}})
	if !strings.Contains(got, "a | (untitled)") || !strings.Contains(got, "prev") || !strings.Contains(got, "2026-01-02") {
		t.Errorf("summary = %q", got)
	}
	_ = fmt.Sprint()
	_ = json.Marshal
}
