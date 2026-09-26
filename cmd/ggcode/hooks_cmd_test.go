package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
)

func sampleHooksConfig() hooks.HookConfig {
	return hooks.HookConfig{
		OnUserMessage: []hooks.Hook{
			{Match: "*", Type: hooks.HookTypeHTTP, URL: "https://audit.example.com/log"},
		},
		PreToolUse: []hooks.Hook{
			{Match: "run_command(git push*)", Command: "echo blocked >&2; exit 2"},
			{Match: "^run_command\\s+git\\s+(push|force)", MatchMode: "regex", Command: "echo git-push-blocked >&2; exit 2"},
		},
		PostToolUse: []hooks.Hook{
			{Match: "write_file|edit_file", Command: "gofmt -w ${FILE_PATH}", InjectOutput: true},
		},
	}
}

func TestWriteHooksListPopulated(t *testing.T) {
	var buf bytes.Buffer
	n, err := writeHooksList(&buf, sampleHooksConfig())
	if err != nil {
		t.Fatalf("writeHooksList: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 hooks counted, got %d", n)
	}
	out := buf.String()
	for _, want := range []string{
		"on_user_message (1)",
		"pre_tool_use (2)",
		"post_tool_use (1)",
		"run_command(git push*)",
		"mode=regex",
		"POST https://audit.example.com/log",
		"ggcode hooks validate",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\n got:\n%s", want, out)
		}
	}
}

func TestWriteHooksListEmpty(t *testing.T) {
	var buf bytes.Buffer
	n, err := writeHooksList(&buf, hooks.HookConfig{})
	if err != nil {
		t.Fatalf("writeHooksList: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 hooks, got %d", n)
	}
	if !strings.Contains(buf.String(), "No hooks configured.") {
		t.Errorf("expected empty-config notice, got:\n%s", buf.String())
	}
}

func TestWriteHooksValidate(t *testing.T) {
	var buf bytes.Buffer
	n, err := writeHooksValidate(&buf, sampleHooksConfig())
	if err != nil {
		t.Fatalf("writeHooksValidate: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 problems for valid config, got %d:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "OK: 4 hook(s) validated") {
		t.Errorf("unexpected OK line:\n%s", buf.String())
	}

	bad := hooks.HookConfig{
		PreToolUse: []hooks.Hook{
			{Match: "write_file", Type: hooks.HookTypeHTTP},           // http requires url
			{Match: "([bad", MatchMode: "regex", Command: "echo hi"},  // invalid regex
			{Match: "edit_file", Command: "true", InjectOutput: true}, // inject_output invalid for pre
		},
	}
	buf.Reset()
	n, err = writeHooksValidate(&buf, bad)
	if err != nil {
		t.Fatalf("writeHooksValidate: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 problems, got %d:\n%s", n, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"requires url", "invalid regex", "inject_output only valid"} {
		if !strings.Contains(out, want) {
			t.Errorf("validation output missing %q\n got:\n%s", want, out)
		}
	}
}

func TestWriteHooksTestTwoSidedVerdicts(t *testing.T) {
	cfg := hooks.HookConfig{
		PreToolUse: []hooks.Hook{
			{Match: "run_command(git push*)", Command: "block-push"},
			{Match: `^run_command\s.*git\s+status`, MatchMode: "regex", Command: "watch-status"},
		},
	}
	// Must-fire side: git push fires the glob rule only.
	var buf bytes.Buffer
	matched, errs, err := writeHooksTest(&buf, cfg, hooks.EventPreToolUse, "run_command", `{"command":"git push --force origin"}`)
	if err != nil {
		t.Fatalf("writeHooksTest: %v", err)
	}
	if matched != 1 || errs != 0 {
		t.Fatalf("push case: want matched=1 errs=0, got matched=%d errs=%d\n%s", matched, errs, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "0  MATCH") || !strings.Contains(out, "1  NO MATCH") {
		t.Errorf("push case should show one MATCH and one NO MATCH:\n%s", out)
	}
	if !strings.Contains(out, "1/2 hook(s) fire on this input") {
		t.Errorf("missing summary line:\n%s", out)
	}
	if !strings.Contains(out, "can block") {
		t.Errorf("pre event should note blocking capability:\n%s", out)
	}

	// Must-not-fire side: git status fires the regex rule, not the glob rule.
	buf.Reset()
	matched, errs, err = writeHooksTest(&buf, cfg, hooks.EventPreToolUse, "run_command", `{"command":"git status"}`)
	if err != nil {
		t.Fatalf("writeHooksTest: %v", err)
	}
	if matched != 1 || errs != 0 {
		t.Fatalf("status case: want matched=1 errs=0, got matched=%d errs=%d\n%s", matched, errs, buf.String())
	}
	out = buf.String()
	if !strings.Contains(out, "0  NO MATCH") || !strings.Contains(out, "1  MATCH") {
		t.Errorf("status case verdicts wrong:\n%s", out)
	}
}

func TestWriteHooksTestInvalidRegexReported(t *testing.T) {
	cfg := hooks.HookConfig{
		PreToolUse: []hooks.Hook{
			{Match: "([bad", MatchMode: "regex", Command: "broken"},
		},
	}
	var buf bytes.Buffer
	matched, errs, err := writeHooksTest(&buf, cfg, hooks.EventPreToolUse, "run_command", "anything")
	if err != nil {
		t.Fatalf("writeHooksTest: %v", err)
	}
	if matched != 0 || errs != 1 {
		t.Fatalf("want matched=0 errs=1, got matched=%d errs=%d\n%s", matched, errs, buf.String())
	}
	if !strings.Contains(buf.String(), "ERROR") || !strings.Contains(buf.String(), "invalid regex") {
		t.Errorf("expected ERROR verdict with reason:\n%s", buf.String())
	}
}

func TestWriteHooksTestPostEventNoBlock(t *testing.T) {
	cfg := hooks.HookConfig{
		PostToolUse: []hooks.Hook{
			{Match: "write_file", Command: "gofmt -w ${FILE_PATH}", InjectOutput: true},
		},
	}
	var buf bytes.Buffer
	matched, _, err := writeHooksTest(&buf, cfg, hooks.EventPostToolUse, "write_file", `{"file_path":"/tmp/a.go"}`)
	if err != nil {
		t.Fatalf("writeHooksTest: %v", err)
	}
	if matched != 1 {
		t.Fatalf("want matched=1, got %d\n%s", matched, buf.String())
	}
	if !strings.Contains(buf.String(), "cannot block") {
		t.Errorf("post event should note non-blocking semantics:\n%s", buf.String())
	}
}

func TestWriteHooksTestInvalidEvent(t *testing.T) {
	var buf bytes.Buffer
	if _, _, err := writeHooksTest(&buf, hooks.HookConfig{}, hooks.EventOnAgentStop, "run_command", "x"); err == nil {
		t.Fatal("expected error for non-tool event")
	}
}

func TestWriteHooksTestNoMatchHint(t *testing.T) {
	cfg := hooks.HookConfig{
		PreToolUse: []hooks.Hook{
			{Match: "run_command(rm -rf *)", Command: "block-rmrf"},
		},
	}
	var buf bytes.Buffer
	matched, _, err := writeHooksTest(&buf, cfg, hooks.EventPreToolUse, "run_command", `{"command":"ls -la"}`)
	if err != nil {
		t.Fatalf("writeHooksTest: %v", err)
	}
	if matched != 0 {
		t.Fatalf("want matched=0, got %d\n%s", matched, buf.String())
	}
	if !strings.Contains(buf.String(), "No hook fires") {
		t.Errorf("expected no-match guidance:\n%s", buf.String())
	}
}
