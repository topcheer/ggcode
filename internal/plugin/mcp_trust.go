package plugin

import (
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/mcptrust"
	"github.com/topcheer/ggcode/internal/safego"
)

// trustTracker is the manager-owned MCP tool-description trust baseline.
// It fingerprints every discovered tool definition, diffs the sighting
// against the persisted baseline (~/.ggcode/mcp-tool-trust.json, first
// sighting seeds silently), and surfaces drift as user-visible notes: a
// tool whose description/schema/annotations silently changed is exactly
// the "rug-pull" injection vector the 2026 MCP security guidance says to
// re-verify on every connection.
//
// Concurrency: plugins connect on independent goroutines (initial connect,
// reconnect watcher, tools/list_changed refresh), so all tracker state is
// mutex-guarded and the store is saved under the same lock.
type trustTracker struct {
	mu    sync.Mutex
	path  string
	store *mcptrust.Store
}

// newTrustTracker loads (or seeds) the baseline store. Returns nil when
// trust checking is disabled (GGCODE_MCP_TRUST=off, no usable home dir) or
// the store cannot be loaded -- a broken baseline must never block startup.
func newTrustTracker() *trustTracker {
	path, ok := mcptrust.ResolvePath()
	if !ok {
		return nil
	}
	store, err := mcptrust.Load(path)
	if err != nil {
		debug.Log("mcp-trust", "baseline load failed (disabled): %v", err)
		return nil
	}
	return &trustTracker{path: path, store: store}
}

// fingerprintDefs converts MCP tool definitions into trust fingerprints.
// Annotations participate with full tri-state fidelity (absent vs false).
func fingerprintDefs(defs []mcp.ToolDefinition) []mcptrust.ToolFingerprint {
	fps := make([]mcptrust.ToolFingerprint, 0, len(defs))
	for _, d := range defs {
		var h mcptrust.Hints
		if d.Annotations != nil {
			h = mcptrust.Hints{
				Title:       d.Annotations.Title,
				ReadOnly:    d.Annotations.ReadOnlyHint,
				Destructive: d.Annotations.DestructiveHint,
				Idempotent:  d.Annotations.IdempotentHint,
				OpenWorld:   d.Annotations.OpenWorldHint,
			}
		}
		fps = append(fps, mcptrust.ToolFingerprint{
			Name: d.Name,
			Hash: mcptrust.Fingerprint(d.Name, d.Description, d.InputSchema, h),
		})
	}
	return fps
}

// check diffs a fresh tool list against the baseline, returns user-facing
// notes for any drift (empty on first sighting -- that seeds the baseline),
// and persists the updated baseline. Best-effort: save failures downgrade
// to a debug log so connect/refresh paths are never blocked by IO.
func (t *trustTracker) check(server string, defs []mcp.ToolDefinition) []string {
	if t == nil {
		return nil
	}
	fps := fingerprintDefs(defs)

	t.mu.Lock()
	defer t.mu.Unlock()

	first := !t.store.Has(server)
	var notes []string
	if !first {
		for _, c := range t.store.Diff(server, fps) {
			notes = append(notes, c.Note())
		}
	}

	if first || len(notes) > 0 {
		t.store.Apply(server, fps, time.Now())
		if err := t.store.Save(t.path); err != nil {
			debug.Log("mcp-trust", "server=%s baseline save failed: %v", server, err)
		}
	}
	if first {
		debug.Log("mcp-trust", "server=%s trust baseline seeded (%d tools)", server, len(fps))
		return nil
	}
	for _, n := range notes {
		debug.Log("mcp-trust", "server=%s DRIFT: %s", server, n)
	}
	return notes
}

// trustNote appends the reset hint once, pointing at the CLI escape hatch.
func trustNotePreamble(notes []string) []string {
	if len(notes) == 0 {
		return notes
	}
	return append(append([]string(nil), notes...),
		fmt.Sprintf("if this change is expected, re-baseline with: ggcode mcp trust --reset <server>"))
}

// recordTrustSnapshot runs the trust check for a connected/refreshed tool
// list and stores the resulting notes on the plugin for Info()/TUI. Runs
// outside m.mu (the tracker has its own lock; Info() re-reads notes under
// m.mu). Failures inside the tracker never affect the connection.
func (m *MCPPlugin) recordTrustSnapshot(tools []mcp.ToolDefinition) {
	if m.trust == nil {
		return
	}
	notes := m.trust.check(m.cfg.Name, tools)
	if len(notes) > 0 {
		notes = trustNotePreamble(notes)
	}
	m.mu.Lock()
	m.trustNotes = notes
	m.mu.Unlock()
}

// recordTrustSnapshotAsync runs the trust check off the caller's critical
// section: Connect holds m.mu (defer unlock) through its return, and the
// tracker does file IO -- both must not block each other.
func (m *MCPPlugin) recordTrustSnapshotAsync(tools []mcp.ToolDefinition) {
	if m.trust == nil {
		return
	}
	safego.Go("plugin.mcp.trust", func() { m.recordTrustSnapshot(tools) })
}

// resetServerBaseline clears one server's persisted baseline so the next
// connection re-seeds silently. Shared by the CLI (`ggcode mcp trust
// --reset`). Best-effort save; errors are returned for the CLI to print.
func resetServerBaseline(server string) error {
	path, ok := mcptrust.ResolvePath()
	if !ok {
		return fmt.Errorf("trust baseline is disabled (GGCODE_MCP_TRUST)")
	}
	store, err := mcptrust.Load(path)
	if err != nil {
		return fmt.Errorf("loading baseline: %w", err)
	}
	if !store.Has(server) {
		return fmt.Errorf("no trust baseline for server %q", server)
	}
	store.Reset(server)
	return store.Save(path)
}

// ResetMCPTrustBaseline clears one server's persisted baseline so the next
// connection re-seeds silently. Exported for the CLI escape hatch
// (`ggcode mcp trust --reset <server>`).
func ResetMCPTrustBaseline(server string) error {
	return resetServerBaseline(server)
}
