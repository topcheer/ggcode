package hooks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// r61 companion tests: structured decision protocol for blocking-event
// hooks (pre_tool_use / on_user_message), mirroring the Claude Code
// permissionDecision convention.

func TestParseHookDecisionUnit(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantDec    string
		wantReason string
	}{
		{"nested claude-code schema", "{\n  \"hookSpecificOutput\": {\n    \"hookEventName\": \"PreToolUse\",\n    \"permissionDecision\": \"deny\",\n    \"permissionDecisionReason\": \"no secrets\"\n  }\n}", DecisionDeny, "no secrets"},
		{"compact schema", `{"permissionDecision":"allow","permissionDecisionReason":"safe"}`, DecisionAllow, "safe"},
		{"shorthand schema", `{"decision":"ask","reason":"confirm"}`, DecisionAsk, "confirm"},
		{"log noise then json", "scanning files...\nok\n{\"decision\":\"deny\",\"reason\":\"later\"}", DecisionDeny, "later"},
		{"no decision", "just some output", "", ""},
		{"unknown decision value", `{"decision":"block","reason":"x"}`, "", ""},
		{"invalid json", "{not json", "", ""},
		{"empty", "", "", ""},
		{"case insensitive", `{"decision":"DENY","reason":"up"}`, DecisionDeny, "up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, reason := parseHookDecision(tc.in)
			if dec != tc.wantDec {
				t.Fatalf("parseHookDecision decision = %q, want %q", dec, tc.wantDec)
			}
			if dec != "" && reason != tc.wantReason {
				t.Fatalf("parseHookDecision reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

func preEnv() HookEnv {
	return HookEnv{
		ToolName:   "write_file",
		RawInput:   `{"path":"x.txt"}`,
		WorkingDir: ".",
	}
}

func TestJSONDenyBlocksPreToolUse(t *testing.T) {
	h := Hook{Match: "*", Command: `echo '{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"no secrets allowed"}}'`}
	res := RunPreHooks([]Hook{h}, preEnv())
	if res.Allowed {
		t.Fatalf("expected deny decision to block, got Allowed=true")
	}
	if res.Decision != DecisionDeny || !strings.Contains(res.DecisionReason, "no secrets") {
		t.Fatalf("Decision=%q Reason=%q, want deny/no-secrets", res.Decision, res.DecisionReason)
	}
	if !strings.Contains(res.Output, "Blocked by pre_tool_use hook") {
		t.Fatalf("Output=%q, want blocked message", res.Output)
	}
}

func TestJSONAllowAndAskDoNotBlock(t *testing.T) {
	allow := Hook{Match: "*", Command: `echo '{"permissionDecision":"allow","permissionDecisionReason":"safe target"}'`}
	res := RunPreHooks([]Hook{allow}, preEnv())
	if !res.Allowed || res.Decision != DecisionAllow || res.DecisionReason != "safe target" {
		t.Fatalf("allow: Allowed=%v Decision=%q Reason=%q", res.Allowed, res.Decision, res.DecisionReason)
	}
	ask := Hook{Match: "*", Command: `echo '{"decision":"ask","reason":"confirm deploy"}'`}
	res = RunPreHooks([]Hook{ask}, preEnv())
	if !res.Allowed || res.Decision != DecisionAsk {
		t.Fatalf("ask: Allowed=%v Decision=%q", res.Allowed, res.Decision)
	}
}

func TestFirstDecisionWins(t *testing.T) {
	hooks := []Hook{
		{Match: "*", Command: `echo '{"decision":"deny","reason":"first hook"}'`},
		{Match: "*", Command: `echo '{"decision":"allow","reason":"second hook"}'`},
	}
	res := RunPreHooks(hooks, preEnv())
	if res.Allowed || res.Decision != DecisionDeny || res.DecisionReason != "first hook" {
		t.Fatalf("Allowed=%v Decision=%q Reason=%q, want first deny to win", res.Allowed, res.Decision, res.DecisionReason)
	}
}

func TestPlainStdoutStillPasses(t *testing.T) {
	h := Hook{Match: "*", Command: `echo 'running pre hook'`}
	res := RunPreHooks([]Hook{h}, preEnv())
	if !res.Allowed || res.Decision != "" {
		t.Fatalf("Allowed=%v Decision=%q, want pass-through with no decision", res.Allowed, res.Decision)
	}
}

func TestLegacyExit2StillBlocks(t *testing.T) {
	h := Hook{Match: "*", Command: "echo blocked-via-stderr >&2; exit 2"}
	res := RunPreHooks([]Hook{h}, preEnv())
	if res.Allowed || res.Decision != "" || !strings.Contains(res.Output, "blocked-via-stderr") {
		t.Fatalf("Allowed=%v Decision=%q Output=%q, want legacy exit-2 block", res.Allowed, res.Decision, res.Output)
	}
}

func TestPostToolUseDenyBecomesPolicyNotice(t *testing.T) {
	env := preEnv()
	env.Event = EventPostToolUse
	env.ToolSuccess = true
	h := Hook{Match: "*", Command: `echo '{"decision":"deny","reason":"too late"}'`}
	res := RunPostHooks([]Hook{h}, env)
	if !res.Allowed {
		t.Fatalf("post_tool_use deny must not block retroactively")
	}
	// #684 convention: on non-blocking events the policy reason reaches the
	// model folded into Output, not as a separate field.
	if !strings.Contains(res.Output, "too late") {
		t.Fatalf("Output=%q, want the policy notice with the hook reason", res.Output)
	}
}

func TestUserMessageDenyBlocks(t *testing.T) {
	h := Hook{Match: "*", Command: `echo '{"decision":"deny","reason":"no deploy prompts"}'`}
	res := RunUserMessageHooks([]Hook{h}, HookEnv{UserMessage: "hi"})
	if res.Allowed || res.Decision != DecisionDeny {
		t.Fatalf("Allowed=%v Decision=%q, want on_user_message deny block", res.Allowed, res.Decision)
	}
}

func TestHTTPJSONDenyBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"permissionDecision":"deny","permissionDecisionReason":"http deny"}`))
	}))
	defer srv.Close()
	h := Hook{Match: "*", Type: HookTypeHTTP, URL: srv.URL}
	res := RunPreHooks([]Hook{h}, preEnv())
	if res.Allowed || res.Decision != DecisionDeny || !strings.Contains(res.DecisionReason, "http deny") {
		t.Fatalf("Allowed=%v Decision=%q Reason=%q, want HTTP 200 deny", res.Allowed, res.Decision, res.DecisionReason)
	}
}

func TestHTTPJSONAllow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"decision":"allow","reason":"webhook ok"}`))
	}))
	defer srv.Close()
	h := Hook{Match: "*", Type: HookTypeHTTP, URL: srv.URL}
	res := RunPreHooks([]Hook{h}, preEnv())
	if !res.Allowed || res.Decision != DecisionAllow {
		t.Fatalf("Allowed=%v Decision=%q, want HTTP 200 allow", res.Allowed, res.Decision)
	}
}
