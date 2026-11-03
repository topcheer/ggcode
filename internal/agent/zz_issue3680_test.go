package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue3680_MCPMutationBumpsEpoch(t *testing.T) {
	l := newTestDedupLedger()
	l.record("run_command", `{"command":"go test ./..."}`, tool.Result{Content: "ok"})

	l.record("mcp__fs__write_file", `{"path":"a.go","content":"x"}`, tool.Result{Content: "written"})
	if got := l.suppressDuplicate("run_command", `{"command":"go test ./..."}`); got != nil {
		t.Fatal("identical command after a mutating MCP write must re-execute, not replay the stale pass")
	}
}

func TestIssue3680_MCPReadOnlyUnchanged(t *testing.T) {
	l := newTestDedupLedger()
	l.record("run_command", `{"command":"cat x"}`, tool.Result{Content: "ok"})

	l.record("web_fetch", `{"url":"https://x"}`, tool.Result{Content: "page"})
	if got := l.suppressDuplicate("run_command", `{"command":"cat x"}`); got == nil {
		t.Fatal("read-only MCP call must not invalidate the command cache")
	}
}
