package hooks

// #566 feature tests: hook env truncation (C) and shell-safe template
// expansion (D).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue566_ShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"plain", "'plain'"},
		{"/tmp/my file.txt", "'/tmp/my file.txt'"}, // spaces stay one word
		{`it's`, `'it'\''s'`},                      // embedded quote
		{"a; rm -rf /", `'a; rm -rf /'`},           // injection neutralized
		{"$(touch pwned)", `'$(touch pwned)'`},     // no expansion
		{"`id`", "'`id`'"},                         // no backtick exec
		{"a\"b", `'a"b'`},                          // double quotes literal
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIssue566_TruncateHookEnv(t *testing.T) {
	// Under limit: untouched, not truncated.
	small := strings.Repeat("a", 1000)
	got, trunc := truncateHookEnv(small)
	if trunc || got != small {
		t.Errorf("small value: trunc=%v changed=%v", trunc, got != small)
	}

	// Over limit: truncated to cap, flag set.
	big := strings.Repeat("a", maxHookEnvValue+1000)
	got, trunc = truncateHookEnv(big)
	if !trunc {
		t.Fatal("oversized value not flagged as truncated")
	}
	if len(got) > maxHookEnvValue {
		t.Errorf("truncated length %d exceeds cap %d", len(got), maxHookEnvValue)
	}

	// Multi-byte runes are never split at the cut point.
	cjk := strings.Repeat("界", maxHookEnvValue/3+50) // 3 bytes per rune
	got, trunc = truncateHookEnv(cjk)
	if !trunc {
		t.Fatal("oversized CJK value not flagged as truncated")
	}
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatalf("rune boundary split produced U+FFFD in truncated value")
		}
		_ = r
	}
}

// TestIssue566_EnvNotTruncatedForNormalSize verifies the happy path: a
// normal-sized RawInput reaches the hook process verbatim via
// GGCODE_RAW_INPUT (guarding against accidental always-truncate).
func TestIssue566_EnvNotTruncatedForNormalSize(t *testing.T) {
	rawInput := `{"file_path":"/tmp/some file.txt","content":"hello"}`
	h := Hook{Command: `test "$GGCODE_RAW_INPUT" != "" && test "$GGCODE_RAW_INPUT_TRUNCATED" = ""`}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "write_file",
		RawInput: rawInput,
	}
	res := executeCommandHook(h, env, BuildPayload(env))
	if res.Err != nil {
		t.Fatalf("normal-size env should pass through untruncated: %v", res.Err)
	}
}

// TestIssue566_LargeRawInputDoesNotBreakHookExec is the core probe for bug C:
// a 1MB RawInput previously made fork/exec fail with E2BIG, so the hook
// process never started — yet Allowed=true was reported and pre_tool_use
// audit hooks were silently bypassed. With truncation the hook must actually
// run, succeed, and see the truncation marker.
func TestIssue566_LargeRawInputDoesNotBreakHookExec(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	big := strings.Repeat("x", 1024*1024) // 1MB — previously E2BIG
	h := Hook{Command: `test "$GGCODE_RAW_INPUT_TRUNCATED" = "1" && test ${#GGCODE_RAW_INPUT} -le 131072 && echo ok`}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "write_file",
		RawInput: big,
		FilePath: "/tmp/big.txt",
	}
	res := executeCommandHook(h, env, BuildPayload(env))
	if res.Err != nil {
		t.Fatalf("hook must run (not E2BIG) with 1MB RawInput: %v", res.Err)
	}
	if out := strings.TrimSpace(res.Output); out != "ok" {
		t.Fatalf("hook output = %q, want ok (truncation marker or size cap missing)", out)
	}
}

// TestIssue566_SpecialCharPathNotMisblocked is the probe for bug D: a path
// containing a quote used to make the hook exit 2 (Allowed=false),
// mis-blocking a legitimate call; a path with spaces used to exit 1.
// Quoted expansion must keep it a single word and exit 0.
func TestIssue566_SpecialCharPathNotMisblocked(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	dir := t.TempDir()
	path := dir + "/it's a file.txt" // both a quote AND spaces
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	// Template uses the BARE $FILE_PATH: shellQuote supplies the quoting.
	// (Wrapping it in "$FILE_PATH" would keep the single quotes literal.)
	h := Hook{Command: `cat $FILE_PATH > /dev/null && echo seen`}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "edit_file",
		FilePath: path,
	}
	res := executeCommandHook(h, env, BuildPayload(env))
	if res.Err != nil {
		t.Fatalf("quoted FILE_PATH must survive shell parsing: %v", res.Err)
	}
	if !res.Allowed {
		t.Fatalf("legitimate call mis-blocked (exit 2 path): %+v", res)
	}
}

// TestIssue566_RawInputInjectionNeutralized: RAW_INPUT content must never
// execute as shell syntax. The sentinel file proves no injection happened.
func TestIssue566_RawInputInjectionNeutralized(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	dir := t.TempDir()
	sentinel := dir + "/pwned566"
	_ = os.Remove(sentinel)
	malicious := "}; touch " + sentinel + " #"
	h := Hook{Command: `printf '%s' "$RAW_INPUT" > /dev/null`}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "write_file",
		RawInput: malicious,
	}
	res := executeCommandHook(h, env, BuildPayload(env))
	if res.Err != nil {
		t.Fatalf("hook failed: %v", res.Err)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("command injection executed! sentinel %s created", sentinel)
	}
}
