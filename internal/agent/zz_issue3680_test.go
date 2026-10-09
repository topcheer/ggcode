package agent

// #3680 probe: a successful mutating mcp__* call must invalidate command
// fingerprints. MCP servers write to the workspace through their own code
// paths (invisible to commandMayRewriteWorkspace's lexical check), so a
// run_command verification replayed within the TTL used to hit the stale
// pass cache.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue3680_MCPMutationBumpsEpoch(t *testing.T) {
	l := newTestDedupLedger()
	l.record("run_command", `{"command":"go test ./..."}`, tool.Result{Content: "ok"})
	// A mutating MCP tool succeeds (its result is cached as mutating, but
	// the old code never bumped the epoch for mcp__*).
	l.record("mcp__fs__write_file", `{"path":"a.go","content":"x"}`, tool.Result{Content: "written"})
	if got := l.suppressDuplicate("run_command", `{"command":"go test ./..."}`); got != nil {
		t.Fatal("identical command after a mutating MCP write must re-execute, not replay the stale pass")
	}
}

func TestIssue3680_MCPReadOnlyUnchanged(t *testing.T) {
	l := newTestDedupLedger()
	l.record("run_command", `{"command":"cat x"}`, tool.Result{Content: "ok"})
	// A read-only (non-MCP) tool call never bumps the epoch - suppression
	// of the cached command must survive (no over-invalidation from reads;
	// note every mcp__* is classified mutating by prefix, so a truly
	// read-only control must be a builtin read tool).
	l.record("web_fetch", `{"url":"https://x"}`, tool.Result{Content: "page"})
	if got := l.suppressDuplicate("run_command", `{"command":"cat x"}`); got == nil {
		t.Fatal("read-only MCP call must not invalidate the command cache")
	}
}
