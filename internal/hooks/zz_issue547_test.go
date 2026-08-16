package hooks

// Issue #547 characteristic tests: runSync error propagation (E), async hook
// survival across caller ctx cancellation (F), and structured-field-only path
// extraction (G).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Bug E: runSync must propagate hook errors ---

func TestIssue547_RunSyncPropagatesHookError(t *testing.T) {
	h := Hook{
		Match:   "write_file",
		Command: "echo boom >&2; exit 1", // failing hook command
	}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file", RawInput: `{}`}
	result := RunPostHooks([]Hook{h}, env)
	if !result.Allowed {
		t.Error("non-blocking hook failure must not set Allowed=false")
	}
	if result.Err == nil {
		t.Fatal("HookResult.Err is nil — hook failure silently swallowed (bug E)")
	}
	if !strings.Contains(result.Err.Error(), "hook command failed") {
		t.Errorf("unexpected error text: %v", result.Err)
	}
}

func TestIssue547_RunSyncJoinsMultipleHookErrors(t *testing.T) {
	hooksList := []Hook{
		{Match: "write_file", Command: "exit 3"},
		{Match: "write_file", Command: "exit 4"},
	}
	env := HookEnv{Event: EventPreToolUse, ToolName: "write_file", RawInput: `{}`}
	result := RunPreHooks(hooksList, env)
	if result.Err == nil {
		t.Fatal("expected joined errors from two failing hooks")
	}
	for _, want := range []string{"exit status 3", "exit status 4"} {
		if !strings.Contains(result.Err.Error(), want) {
			t.Errorf("joined error missing %q: %v", want, result.Err)
		}
	}
}

func TestIssue547_RunSyncSuccessKeepsErrNil(t *testing.T) {
	h := Hook{Match: "write_file", Command: "echo fine"}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file", RawInput: `{}`}
	result := RunPostHooks([]Hook{h}, env)
	if result.Err != nil {
		t.Errorf("successful hook must leave Err nil, got %v", result.Err)
	}
	if result.Allowed != true {
		t.Error("expected Allowed=true")
	}
}

// --- Bug F: async stop hooks must survive caller ctx cancellation ---

func TestIssue547_AsyncStopHookSurvivesCallerCtxCancel(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller cancels BEFORE the stop hook runs (teardown ordering)

	cfg := HookConfig{
		OnAgentStop: []Hook{
			{Match: "*", Command: fmt.Sprintf("sleep 1 && echo done > %s", marker)},
		},
	}
	env := HookEnv{
		Event:     EventOnAgentStop,
		Ctx:       ctx, // pre-cancelled — must not kill the async hook
		SessionID: "issue547",
	}
	Dispatch(cfg, env)

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // hook completed despite ctx cancellation
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("marker file not written within 6s — async on_agent_stop hook was killed by cancelled ctx (bug F)")
}

// --- Bug G: ExtractFilePath must not scan content values ---

func TestIssue547_ExtractFilePathIgnoresContentPaths(t *testing.T) {
	// write_file whose content embeds a JSON config example containing a
	// "path" key. The real target is file_path; the example must not win.
	raw := `{"file_path":"/real/target.go","content":"{\n  \"path\": \"/etc/example/config.yaml\",\n  \"level\": 2\n}\n"}`
	got := ExtractFilePath("write_file", raw)
	if got != "/real/target.go" {
		t.Errorf("ExtractFilePath = %q, want /real/target.go — content-embedded path leaked (bug G)", got)
	}
}

func TestIssue547_ExtractFilePathContentOnlyReturnsEmpty(t *testing.T) {
	// No real path field at all — only an example inside content.
	raw := `{"content":"write config to \"path\": \"/etc/example\" then reload"}`
	if got := ExtractFilePath("write_file", raw); got != "" {
		t.Errorf("ExtractFilePath = %q, want empty — content substring must not be scanned", got)
	}
}

func TestIssue547_ExtractFilePathStructuredFields(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"file_path":"/tmp/test.go","content":"hello"}`, "/tmp/test.go"},
		{`{"path":"src/main.go"}`, "src/main.go"},
		{`{"file":"/etc/config.yaml"}`, "/etc/config.yaml"},
		{`{"command":"ls -la"}`, ""},
		{`{"file_path":null,"content":"hello"}`, ""},
		{`{"file_path":42,"content":"hello"}`, ""},
		{`{"file_path":true,"content":"hello"}`, ""},
		{`{"file_path":null,"path":"/real/path"}`, "/real/path"},
		{`not json at all`, ""},
		{``, ""},
	}
	for _, tc := range cases {
		if got := ExtractFilePath("write_file", tc.raw); got != tc.want {
			t.Errorf("ExtractFilePath(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestIssue547_ExtractFilePathEditOldNewText(t *testing.T) {
	// edit_file old_text/new_text may legitimately contain path-like strings;
	// they are content, not the target. file_path must win.
	raw := `{"file_path":"/app/main.go","old_text":"open(\"path\": \"/old\")","new_text":"open(\"path\": \"/new\")"}`
	if got := ExtractFilePath("edit_file", raw); got != "/app/main.go" {
		t.Errorf("ExtractFilePath = %q, want /app/main.go", got)
	}
}
