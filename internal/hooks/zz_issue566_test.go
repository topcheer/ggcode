package hooks

import (
	"os"
	"strings"
	"testing"
)

func TestIssue566_ShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"plain", "'plain'"},
		{"/tmp/my file.txt", "'/tmp/my file.txt'"},
		{`it's`, `'it'\''s'`},
		{"a; rm -rf /", `'a; rm -rf /'`},
		{"$(touch pwned)", `'$(touch pwned)'`},
		{"`id`", "'`id`'"},
		{"a\"b", `'a"b'`},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIssue566_TruncateHookEnv(t *testing.T) {

	small := strings.Repeat("a", 1000)
	got, trunc := truncateHookEnv(small)
	if trunc || got != small {
		t.Errorf("small value: trunc=%v changed=%v", trunc, got != small)
	}

	big := strings.Repeat("a", maxHookEnvValue+1000)
	got, trunc = truncateHookEnv(big)
	if !trunc {
		t.Fatal("oversized value not flagged as truncated")
	}
	if len(got) > maxHookEnvValue {
		t.Errorf("truncated length %d exceeds cap %d", len(got), maxHookEnvValue)
	}

	cjk := strings.Repeat("界", maxHookEnvValue/3+50)
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

func TestIssue566_LargeRawInputDoesNotBreakHookExec(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	big := strings.Repeat("x", 1024*1024)
	h := Hook{Command: `test "$GGCODE_RAW_INPUT_TRUNCATED" = "1" && test ${#GGCODE_RAW_INPUT} -le 65536 && echo ok`}
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

func TestIssue566_SpecialCharPathNotMisblocked(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell process")
	}
	dir := t.TempDir()
	path := dir + "/it's a file.txt"
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

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
