package agent

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func newTestDedupLedger() *toolDedupLedger {
	l := newToolDedupLedger()
	l.ttl = 2 * time.Second
	return l
}

func TestToolDedupSuppressesIdenticalMutatingCall(t *testing.T) {
	l := newTestDedupLedger()
	if got := l.suppressDuplicate("git_commit", `{"message":"x"}`); got != nil {
		t.Fatalf("expected no suppression before first execution, got %+v", got)
	}
	l.record("git_commit", `{"message":"x"}`, tool.Result{Content: "committed abc123"})
	got := l.suppressDuplicate("git_commit", `{"message":"x"}`)
	if got == nil {
		t.Fatal("expected duplicate to be suppressed")
	}
	if !strings.Contains(got.Content, "suppressed-as-duplicate") || !strings.Contains(got.Content, "committed abc123") {
		t.Fatalf("suppressed result must replay prior output with notice, got: %q", got.Content)
	}
	if got.IsError {
		t.Fatal("suppressed result must not be an error")
	}
}

func TestToolDedupIgnoresReadOnlyTools(t *testing.T) {
	l := newTestDedupLedger()
	l.record("grep", `{"pattern":"x"}`, tool.Result{Content: "matches"})
	if got := l.suppressDuplicate("grep", `{"pattern":"x"}`); got != nil {
		t.Fatalf("read-only tools must never be suppressed, got %+v", got)
	}
}

func TestToolDedupIgnoresErrorResults(t *testing.T) {
	l := newTestDedupLedger()
	l.record("run_command", `{"command":"go test"}`, tool.Result{Content: "boom", IsError: true})
	if got := l.suppressDuplicate("run_command", `{"command":"go test"}`); got != nil {
		t.Fatal("failed mutating calls must remain retryable")
	}
}

func TestToolDedupEpochInvalidatesAfterFileEdit(t *testing.T) {
	l := newTestDedupLedger()
	l.record("run_command", `{"command":"go test ./..."}`, tool.Result{Content: "ok"})
	l.record("edit_file", `{"path":"a.go"}`, tool.Result{Content: "edited"})
	if got := l.suppressDuplicate("run_command", `{"command":"go test ./..."}`); got != nil {
		t.Fatal("identical command after a file edit must re-execute (verify loop), not be suppressed")
	}
}

func TestToolDedupExpiresAfterTTL(t *testing.T) {
	l := newTestDedupLedger()
	l.record("git_commit", `{"message":"x"}`, tool.Result{Content: "ok"})
	// Simulate TTL expiry by rewriting the recorded timestamp.
	for fp, e := range l.table {
		e.at = time.Now().Add(-3 * time.Second)
		l.table[fp] = e
	}
	if got := l.suppressDuplicate("git_commit", `{"message":"x"}`); got != nil {
		t.Fatal("duplicate outside TTL window must re-execute")
	}
}

func TestToolDedupMCPPrefixTreatedAsMutating(t *testing.T) {
	l := newTestDedupLedger()
	if !isMutatingTool("mcp__github__create_pull_request") {
		t.Fatal("mcp__ tools must be treated as mutating")
	}
	l.record("mcp__github__create_pull_request", `{"title":"t"}`, tool.Result{Content: "PR #1"})
	if got := l.suppressDuplicate("mcp__github__create_pull_request", `{"title":"t"}`); got == nil {
		t.Fatal("duplicate MCP mutating call must be suppressed")
	}
}

func TestToolDedupBoundedEntries(t *testing.T) {
	l := newTestDedupLedger()
	for i := 0; i < toolDedupMaxEntries+10; i++ {
		args := `{"n":` + string(rune('a'+i%26)) + `,"i":` + strconv.Itoa(i) + `}`
		l.record("git_tag", args, tool.Result{Content: "ok"})
	}
	if l.count > toolDedupMaxEntries {
		t.Fatalf("ledger must stay bounded: %d > %d", l.count, toolDedupMaxEntries)
	}
}
