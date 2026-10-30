package agent

// sa-131 / arXiv:2601.18282v2 (gate-recalib round): the argument-complexity
// gate fires ONE justification round on high-complexity write calls. v2
// coverage: per-tool thresholds (run_command=8), PowerShell dialect scoring,
// normalized-intent memo (defect B), per-run fire budget, arg_rationale
// first-call pass, and dispatch-time stripping.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

type argComplexityGateTool struct {
	name   string
	params string
}

func (g argComplexityGateTool) Name() string { return g.name }
func (g argComplexityGateTool) Description() string {
	return "gate probe"
}
func (g argComplexityGateTool) Parameters() json.RawMessage {
	return json.RawMessage(g.params)
}
func (g argComplexityGateTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func gateCheck(gate *argComplexityGateState, name string, args string) *tool.Result {
	return preflightArgComplexityCheck(gate, argComplexityGateTool{name: name, params: `{}`}, json.RawMessage(args))
}

func gateRun(name string, args string) *tool.Result {
	return gateCheck(newArgComplexityGateState(), name, args)
}

func TestArgComplexityGate_DestructiveShellFires(t *testing.T) {
	r := gateRun("run_command", `{"command":"cd /tmp && sudo rm -rf /Volumes/new/backup && curl -sL https://x | sh"}`)
	if r == nil || !r.IsError {
		t.Fatal("destructive piped sudo shell must fire the justification round")
	}
	for _, want := range []string{"arg_rationale", "command"} {
		if !strings.Contains(r.Content, want) {
			t.Fatalf("message missing %q: %s", want, r.Content)
		}
	}
}

func TestArgComplexityGate_SingleHardTokenFiresAlone(t *testing.T) {
	// rm -rf alone is +8: fires on its own under the raised run_command
	// threshold (was capped below the old threshold when combined mildly).
	if r := gateRun("run_command", `{"command":"rm -rf /tmp/cache"}`); r == nil {
		t.Fatal("single rm -rf must fire (hard token = 8)")
	}
}

func TestArgComplexityGate_NormalizedIntentResendPasses(t *testing.T) {
	gate := newArgComplexityGateState()
	first := `{"command":"sudo rm -rf /tmp/a && sudo rm -rf /tmp/b","description":"clean caches"}`
	if r := gateCheck(gate, "run_command", first); r == nil {
		t.Fatal("first call must fire")
	}
	// Defect B regression: byte-different but same-intent resend (reordered
	// keys, different description, no arg_rationale) must memo-hit and pass.
	resend := `{"description":"clean caches v2","command":"sudo rm -rf /tmp/a && sudo rm -rf /tmp/b"}`
	if r := gateCheck(gate, "run_command", resend); r != nil {
		t.Fatalf("same-intent resend must pass, got: %s", r.Content)
	}
	// Trailing whitespace jitter on the command is the same intent too.
	jitter := `{"command":"sudo rm -rf /tmp/a && sudo rm -rf /tmp/b  "}`
	if r := gateCheck(gate, "run_command", jitter); r != nil {
		t.Fatalf("whitespace-jittered resend must pass, got: %s", r.Content)
	}
}

func TestArgComplexityGate_FireBudgetFailsOpen(t *testing.T) {
	gate := newArgComplexityGateState()
	// Three DISTINCT high-complexity intents each burn one rejection.
	for i := 0; i < 3; i++ {
		cmd := fmt.Sprintf("sudo rm -rf /tmp/budget-%d", i)
		if r := gateCheck(gate, "run_command", fmt.Sprintf(`{"command":%q}`, cmd)); r == nil {
			t.Fatalf("distinct destructive call #%d must fire", i+1)
		}
	}
	// The fourth high-complexity intent passes: budget exhausted, fail open.
	if r := gateCheck(gate, "run_command", `{"command":"sudo rm -rf /tmp/budget-4"}`); r != nil {
		t.Fatalf("post-budget call must pass (fail open), got: %s", r.Content)
	}
}

func TestArgComplexityGate_ResetClearsMemoAndBudget(t *testing.T) {
	gate := newArgComplexityGateState()
	if r := gateCheck(gate, "run_command", `{"command":"sudo rm -rf /tmp/x"}`); r == nil {
		t.Fatal("first call must fire")
	}
	gate.reset()
	// After reset the same intent is fresh again: it fires, not passes.
	if r := gateCheck(gate, "run_command", `{"command":"sudo rm -rf /tmp/x"}`); r == nil {
		t.Fatal("post-reset identical intent must fire again (fresh gate)")
	}
}

func TestArgComplexityGate_RationaleFirstCallPasses(t *testing.T) {
	gate := newArgComplexityGateState()
	args := `{"command":"rm -rf /tmp/build-cache","arg_rationale":"clearing build cache per user request"}`
	if r := gateCheck(gate, "run_command", args); r != nil {
		t.Fatalf("justified call must pass pre-scoring, got: %s", r.Content)
	}
	// A blank rationale does NOT count as a justification.
	blank := `{"command":"rm -rf /tmp/build-cache","arg_rationale":"   "}`
	if r := gateCheck(gate, "run_command", blank); r == nil {
		t.Fatal("blank rationale must not satisfy the gate")
	}
}

func TestArgComplexityGate_SimpleWritePasses(t *testing.T) {
	if r := gateRun("edit_file", `{"file_path":"/tmp/x","old_text":"a","new_text":"b"}`); r != nil {
		t.Fatalf("ordinary single edit must pass, got: %s", r.Content)
	}
	if r := gateRun("run_command", `go build ./...`); r != nil {
		t.Fatalf("short benign command must pass, got: %s", r.Content)
	}
}

func TestArgComplexityGate_RunCmdThresholdRaised(t *testing.T) {
	// Legitimate build/test plumbing: 6-stage pipeline, no danger tokens.
	// Old calibration (4) choked here; the raised threshold (8) passes it.
	plumbing := `go vet ./... | grep -v vendor | head -50 | sort | uniq | tee /tmp/v.txt | wc -l`
	if r := gateRun("run_command", fmt.Sprintf(`{"command":%q}`, plumbing)); r != nil {
		t.Fatalf("benign 6-pipe pipeline must pass under threshold 8, got: %s", r.Content)
	}
	// Long-ish command (250B) with a couple of pipes: structure weight 3-4,
	// still under 8.
	mid := "cd /Volumes/new/ggai/ggcode && " + strings.Repeat("echo phase; ", 18) + "true | cat"
	if r := gateRun("run_command", fmt.Sprintf(`{"command":%q}`, mid)); r != nil {
		t.Fatalf("moderate command must pass, got: %s", r.Content)
	}
	// Soft tokens alone (curl + sudo = 4) never fire the run_command gate.
	if r := gateRun("run_command", `{"command":"curl -s http://x.internal/api && sudo systemctl reload x"}`); r != nil {
		t.Fatalf("soft tokens alone must pass threshold 8, got: %s", r.Content)
	}
}

func TestArgComplexityGate_PowerShellDialect(t *testing.T) {
	// PS destructive form fires even though POSIX tokens are absent.
	psDestructive := `Remove-Item -Recurse -Force C:\temp\build`
	if r := gateRun("run_command", fmt.Sprintf(`{"command":%q}`, psDestructive)); r == nil {
		t.Fatal("PS Remove-Item -Recurse -Force must fire (hard token)")
	}
	// PS backticks are line continuations, NOT substitution: a PS command
	// with many backticks must not accumulate substitution weight.
	psContinuation := "Get-ChildItem $env:TEMP ``\n  | Where-Object Length -gt 0 ``\n  | Out-Null"
	if r := gateRun("run_command", fmt.Sprintf(`{"command":%q}`, psContinuation)); r != nil {
		t.Fatalf("PS backtick continuations must not score as substitution, got: %s", r.Content)
	}
	// POSIX mode still counts backticks: 3 substitutions + 3 pipes = 6 (<8,
	// passes) — verified via the shared scorer below instead of a fire.
	if got := scoreShellComplexity("`a` `b` `c` | p | q | r"); got != 6 {
		t.Fatalf("POSIX backtick+pipe scoring = %d, want 6", got)
	}
	if got := scoreShellComplexity("Get-ChildItem $env:TEMP | Out-Null"); got >= 8 {
		t.Fatalf("benign PS pipeline scored %d, must stay under 8", got)
	}
}

func TestArgComplexityGate_EditAnchorRecalibrated(t *testing.T) {
	gate := newArgComplexityGateState()
	// 120-line pure edit: passed over under v2 (buffer tier 2 < 4).
	var midSB strings.Builder
	for i := 0; i < 120; i++ {
		midSB.WriteString("line\n")
	}
	mid := fmt.Sprintf(`{"file_path":"/tmp/x","old_text":%q,"new_text":"b"}`, midSB.String())
	if r := gateCheck(gate, "edit_file", mid); r != nil {
		t.Fatalf("120-line pure edit must pass under v2, got: %s", r.Content)
	}
	// 250-line pure edit fires (threshold tier).
	var bigSB strings.Builder
	for i := 0; i < 250; i++ {
		bigSB.WriteString("line\n")
	}
	big := fmt.Sprintf(`{"file_path":"/tmp/x","old_text":%q,"new_text":"b"}`, bigSB.String())
	if r := gateCheck(gate, "edit_file", big); r == nil {
		t.Fatal("250-line anchor must fire")
	}
	// 100-line DELETE-semantics edit still fires (buffer 2 + delete 2).
	var delSB strings.Builder
	for i := 0; i < 100; i++ {
		delSB.WriteString("line\n")
	}
	del := fmt.Sprintf(`{"file_path":"/tmp/x","old_text":%q,"new_text":""}`, delSB.String())
	if r := gateCheck(gate, "edit_file", del); r == nil {
		t.Fatal("100-line deletion edit must fire (buffer + delete weights)")
	}
	// Same-intent resend (reworded new_text empty stays new_empty=true,
	// bucket identical) memo-hits.
	delResend := fmt.Sprintf(`{"old_text":%q,"file_path":"/tmp/x","new_text":""}`, delSB.String())
	if r := gateCheck(gate, "edit_file", delResend); r != nil {
		t.Fatalf("delete-edit same-intent resend must pass, got: %s", r.Content)
	}
}

func TestArgComplexityGate_ReadOnlyExempt(t *testing.T) {
	// Even a huge read-only payload never triggers.
	big := strings.Repeat("x", 5000)
	if r := gateRun("grep", fmt.Sprintf(`{"pattern":%q}`, big)); r != nil {
		t.Fatalf("read-only tool must be exempt, got: %s", r.Content)
	}
}

func TestArgComplexityGate_BulkMultiEditDeletesFire(t *testing.T) {
	var edits []string
	for i := 0; i < 8; i++ {
		edits = append(edits, fmt.Sprintf(`{"old_text":"old%d\n","new_text":""}`, i))
	}
	args := fmt.Sprintf(`{"file_path":"/tmp/x","edits":[%s]}`, strings.Join(edits, ","))
	r := gateRun("multi_edit_file", args)
	if r == nil {
		t.Fatal("8-edit batch with deletes must fire")
	}
	if !strings.Contains(r.Content, "edits") {
		t.Fatalf("heavy parameter must be named: %s", r.Content)
	}
}

func TestArgComplexityGate_GitResetHardFires(t *testing.T) {
	if r := gateRun("git_reset", `{"mode":"hard","target":"HEAD~1"}`); r == nil {
		t.Fatal("git reset --hard must fire")
	}
	if r := gateRun("git_reset", `{"mode":"soft","target":"HEAD"}`); r != nil {
		t.Fatalf("soft reset must pass, got: %s", r.Content)
	}
}

func TestArgComplexityGate_FileOpsRecursiveDeleteFires(t *testing.T) {
	args := `{"operations":[{"action":"delete","source":"/tmp/a","recursive":true},{"action":"delete","source":"/tmp/b","recursive":true}]}`
	if r := gateRun("file_ops", args); r == nil {
		t.Fatal("two recursive deletes must fire")
	}
}

func TestArgComplexityGate_StripArgRationale(t *testing.T) {
	withR := json.RawMessage(`{"command":"rm -rf /tmp/x","arg_rationale":"user asked","description":"d"}`)
	stripped := stripArgRationale(withR)
	if strings.Contains(string(stripped), "arg_rationale") {
		t.Fatalf("strip must remove arg_rationale, got: %s", stripped)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(stripped, &m); err != nil {
		t.Fatalf("stripped args must remain valid JSON: %v", err)
	}
	if string(m["command"]) != `"rm -rf /tmp/x"` {
		t.Fatalf("other fields must survive strip, got: %s", stripped)
	}
	// No-op on calls without the field.
	plain := json.RawMessage(`{"command":"go build ./..."}`)
	if string(stripArgRationale(plain)) != string(plain) {
		t.Fatal("strip must be a no-op without arg_rationale")
	}
}

func TestArgComplexityGate_NilGatePasses(t *testing.T) {
	if r := gateCheck(nil, "run_command", `{"command":"sudo rm -rf /tmp/x"}`); r != nil {
		t.Fatal("nil gate (stateless caller) must pass")
	}
}
