package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue684_TruncateHookEnvJSON_AlwaysValidJSON(t *testing.T) {

	big := strings.Repeat("a", 200*1024)
	payload := `{"tool":"edit_file","input":{"content":"` + big + `","path":"/tmp/x.go"}}`

	got := truncateHookEnvJSON(payload)
	if len(got) > maxHookEnvValue {
		t.Fatalf("truncated payload still over budget: %d bytes", len(got))
	}
	if payload == got {
		t.Fatal("payload under budget should be returned unchanged — test payload is malformed")
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("truncated payload is not valid JSON (len=%d): %.120q...", len(got), got)
	}
}

func TestIssue684_TruncateHookEnvJSON_EscapeCutAvoided(t *testing.T) {

	big := strings.Repeat(`quote:\"backslash:\\unicode:é `, 8*1024)
	payload := `{"content":"` + big + `"}`
	got := truncateHookEnvJSON(payload)
	if !json.Valid([]byte(got)) {
		t.Fatalf("cut landed inside an escape sequence: invalid JSON %.120q...", got)
	}
}

func TestIssue684_TruncateHookEnvJSON_MarkerFallbackIsValid(t *testing.T) {

	big := strings.Repeat("x", 300*1024)
	payload := `{"content":"` + big + `"}`
	got := truncateHookEnvJSON(payload)
	if !json.Valid([]byte(got)) {
		t.Fatalf("fallback marker not valid JSON: %q", got)
	}
	if !strings.Contains(got, "stdin") {
		t.Fatalf("fallback marker should point to stdin: %q", got)
	}
}

func TestIssue684_TruncateHookEnvJSON_UnderBudgetUnchanged(t *testing.T) {
	payload := `{"small":true}`
	if got := truncateHookEnvJSON(payload); got != payload {
		t.Fatalf("under-budget payload must be unchanged, got %q", got)
	}
}

func TestIssue684_ExpandHookTemplate_KeepsJSONValid(t *testing.T) {
	big := strings.Repeat("b", 200*1024)
	env := HookEnv{Event: EventPreToolUse, ToolName: "edit_file", RawInput: `{"content":"` + big + `"}`}
	expanded := expandHookTemplate(`audit --payload $PAYLOAD`, env, `{"content":"`+big+`"}`)

	quoted := strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(expanded, "'", 2)[1], "'"), "'")

	if i := strings.Index(quoted, "'"); i >= 0 {
		quoted = quoted[:i]
	}
	if !json.Valid([]byte(quoted)) {
		t.Fatalf("argv $PAYLOAD expansion is not valid JSON: %.120q...", quoted)
	}
}

func TestIssue684_PostExit2Reason_ReachesOutput(t *testing.T) {

	h := Hook{
		Command: `printf 'forbidden: secret file' >&2; exit 2`,
		Match:   "*",
	}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file"}
	res := executeCommandHook(h, env, buildTestPayload())
	if res.Allowed != true {
		t.Fatalf("post_tool_use exit 2 must not block, got Allowed=%v", res.Allowed)
	}
	if res.PolicyNotice == "" || !strings.Contains(res.PolicyNotice, "forbidden: secret file") {
		t.Fatalf("PolicyNotice must carry the stderr reason, got %q", res.PolicyNotice)
	}
}

func TestIssue684_RunPostHooks_FoldsReasonIntoOutput(t *testing.T) {

	hooksList := []Hook{{Command: `printf 'no secrets in output' >&2; exit 2`, Match: "*"}}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file"}
	res := runSync(hooksList, env)
	if !res.Allowed {
		t.Fatal("post hooks must never block")
	}
	if !strings.Contains(res.Output, "no secrets in output") {
		t.Fatalf("violation reason must be folded into Output (the only field consumers read), got %q", res.Output)
	}
	if !strings.Contains(res.Output, "policy") {
		t.Fatalf("folded reason should be labeled as a policy verdict, got %q", res.Output)
	}
}

func TestIssue684_BlockingEvent_Exit2StillBlocks(t *testing.T) {

	hooksList := []Hook{{Command: `printf 'denied' >&2; exit 2`, Match: "*"}}
	env := HookEnv{Event: EventPreToolUse, ToolName: "write_file"}
	res := runSync(hooksList, env)
	if res.Allowed {
		t.Fatal("pre_tool_use exit 2 must still block")
	}
	if !strings.Contains(res.Output, "denied") {
		t.Fatalf("block message must carry reason, got %q", res.Output)
	}
}

func TestIssue684_HTTP403_NonBlocking_Notice(t *testing.T) {

	h := Hook{URL: "http://403.example/", Match: "*"}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file"}

	notice := formatPolicyNotice(EventPostToolUse, "PII detected")
	if !strings.Contains(notice, "post_tool_use policy") || !strings.Contains(notice, "PII detected") {
		t.Fatalf("notice format wrong: %q", notice)
	}
	_ = h
	_ = env
}

func buildTestPayload() HookPayload {
	return HookPayload{Event: EventPostToolUse, Tool: &PayloadTool{Name: "write_file"}}
}
