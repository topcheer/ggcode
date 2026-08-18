package hooks

// #679 regression tests: tail-* path anchoring in the matcher, argv-side
// truncation for $RAW_INPUT/$PAYLOAD expansion, and event-aware block
// semantics for exit 2 / HTTP 403 (post_tool_use never blocks).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// --- Defect 1: trailing `*` must anchor on the extracted path ---

func TestIssue679_TailStarAnchorsOnPath(t *testing.T) {
	cases := []struct {
		name          string
		pattern, tool string
		input         string
		want          bool
	}{
		{
			// Old code matched the ENTIRE argument JSON: the content field
			// mentioning "internal/" made edit_file(internal/*) fire and an
			// exit-2 audit hook mis-blocked the call.
			name:    "content substring must not match",
			pattern: "edit_file(internal/*)",
			tool:    "edit_file",
			input:   `{"file_path":"/src/other/main.go","new_text":"see internal/ docs"}`,
			want:    false,
		},
		{
			name:    "real path prefix matches",
			pattern: "edit_file(internal/*)",
			tool:    "edit_file",
			input:   `{"file_path":"internal/agent/foo.go","new_text":"x"}`,
			want:    true,
		},
		{
			name:    "path containing-but-not-prefixed must not match",
			pattern: "edit_file(internal/*)",
			tool:    "edit_file",
			input:   `{"file_path":"/Volumes/internal-ish/x.go"}`,
			want:    false,
		},
		{
			// No structured path → legacy fallback over raw JSON (pinned by
			// runner_test.go: run_command(git commit *)).
			name:    "no extractable path falls back to contains",
			pattern: "run_command(git commit *)",
			tool:    "run_command",
			input:   `{"command":"git commit -m test"}`,
			want:    true,
		},
	}
	for _, tc := range cases {
		if got := matchTool(tc.pattern, tc.tool, tc.input); got != tc.want {
			t.Errorf("%s: matchTool(%q, %q) = %v, want %v", tc.name, tc.pattern, tc.input, got, tc.want)
		}
	}
}

func TestIssue679_TailStarNoMisBlockEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	h := Hook{Match: "edit_file(internal/*)", Command: "exit 2"}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "edit_file",
		RawInput: `{"file_path":"/src/other/main.go","new_text":"see internal/ docs"}`,
	}
	res := RunPreHooks([]Hook{h}, env)
	if !res.Allowed {
		t.Fatalf("audit hook mis-blocked on content substring: %+v", res)
	}
}

func TestIssue679_TailStarStillBlocksRealPath(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	h := Hook{Match: "edit_file(internal/*)", Command: "echo denied >&2; exit 2"}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "edit_file",
		RawInput: `{"file_path":"internal/agent/foo.go","new_text":"y"}`,
	}
	res := RunPreHooks([]Hook{h}, env)
	if res.Allowed {
		t.Fatal("real internal/ path prefix must still block via exit 2")
	}
	if !strings.Contains(res.Output, "denied") {
		t.Errorf("block reason missing from output: %q", res.Output)
	}
}

// --- Defect 2: $RAW_INPUT/$PAYLOAD argv expansion capped like envp ---

// runArgvSizeProbe expands $VAR via the hook template and measures the
// byte count the shell actually receives for that argument.
func runArgvSizeProbe(t *testing.T, tmpl, varName string, env HookEnv) int {
	t.Helper()
	h := Hook{Match: "*", Command: `printf '%s' $` + varName + ` | wc -c | tr -d ' '`}
	env.Event = EventPreToolUse
	res := executeCommandHook(h, env, BuildPayload(env))
	if res.Err != nil {
		t.Fatalf("hook with oversized %s failed to run: %v", varName, res.Err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(res.Output))
	if err != nil {
		t.Fatalf("unexpected probe output %q: %v", res.Output, err)
	}
	return n
}

func TestIssue679_RawInputArgvTruncated(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	big := strings.Repeat("x", 512*1024) // 512KB — over MAX_ARG_STRLEN verbatim
	env := HookEnv{ToolName: "write_file", RawInput: big, FilePath: "/tmp/big.txt"}
	n := runArgvSizeProbe(t, "", "RAW_INPUT", env)
	if n == 0 || n > maxHookEnvValue {
		t.Errorf("$RAW_INPUT argv = %d bytes, want 0 < n <= %d (truncation missing)", n, maxHookEnvValue)
	}
}

func TestIssue679_PayloadArgvTruncated(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	env := HookEnv{
		ToolName: "write_file",
		RawInput: `{"file_path":"/tmp/x.go","content":"` + strings.Repeat("y", 300*1024) + `"}`,
	}
	n := runArgvSizeProbe(t, "", "PAYLOAD", env)
	if n == 0 || n > maxHookEnvValue {
		t.Errorf("$PAYLOAD argv = %d bytes, want 0 < n <= %d (truncation missing)", n, maxHookEnvValue)
	}
}

func TestIssue679_NormalSizeArgvUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	raw := `{"file_path":"/tmp/f.txt","content":"hello world"}`
	// Bare $RAW_INPUT: shellQuote (inside template expansion) supplies the quoting.
	h := Hook{Command: `test $RAW_INPUT = ` + shellQuote(raw) + " && echo verbatim"}
	env := HookEnv{Event: EventPreToolUse, ToolName: "write_file", RawInput: raw}
	res := executeCommandHook(h, env, BuildPayload(env))
	if res.Err != nil {
		t.Fatalf("normal-size $RAW_INPUT must pass through verbatim: %v", res.Err)
	}
	if got := strings.TrimSpace(res.Output); got != "verbatim" {
		t.Errorf("output = %q, want verbatim", got)
	}
}

// --- Defect 3: post_tool_use exit 2 / HTTP 403 must not short-circuit ---

func TestIssue679_PostToolUseExit2KeepsInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	hooks := []Hook{
		{Match: "write_file", Command: "echo first", InjectOutput: true},
		{Match: "write_file", Command: "echo reason >&2; exit 2", InjectOutput: true},
		{Match: "write_file", Command: "echo third", InjectOutput: true},
	}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file", RawInput: `{}`}
	res := RunPostHooks(hooks, env)
	if !res.Allowed {
		t.Fatalf("post_tool_use exit 2 must not block: %+v", res)
	}
	for _, want := range []string{"first", "third"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("injected output missing %q (short-circuit dropped it): %q", want, res.Output)
		}
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "exit status 2") {
		t.Errorf("exit 2 on post hook must surface as non-blocking Err, got %v", res.Err)
	}
}

func TestIssue679_PostToolUseHTTP403NonBlocking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "policy violation")
	}))
	defer server.Close()

	h := Hook{Match: "*", Type: HookTypeHTTP, URL: server.URL}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file", RawInput: `{}`}
	res := executeHTTPHook(h, env, BuildPayload(env))
	if !res.Allowed {
		t.Fatalf("HTTP 403 on post_tool_use must not block: %+v", res)
	}
	// #684 supersedes the old "non-blocking Err" channel: post-tool consumers
	// read only Output, so the reason now travels via PolicyNotice (folded
	// into Output by runSync) instead of a dropped Err.
	if res.PolicyNotice == "" || !strings.Contains(res.PolicyNotice, "policy violation") {
		t.Errorf("403 reason must surface via PolicyNotice, got %q", res.PolicyNotice)
	}
}

func TestIssue679_OnUserMessageExit2StillBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	h := Hook{Match: "*", Command: "exit 2"}
	env := HookEnv{ToolName: "", RawInput: `{}`}
	res := RunUserMessageHooks([]Hook{h}, env)
	if res.Allowed {
		t.Fatal("on_user_message exit 2 must still block (isBlockingEvent must include it)")
	}
}
