package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Harness Fingerprint: scaffolding-regression attribution for agent runs.
//
// Research basis (frontier agent literature, 2026):
//   - arXiv:2607.03691 "Don't Blame the Large Language Model: How Scaffolding
//     Evolution Shapes Coding Agent Quality" — across 35 releases of a coding
//     agent with a FIXED underlying model, the resolve rate fluctuated between
//     23% and 39% with no upward trend while per-task token spend rose by 70%+
//     and completion time doubled. The swings tracked scaffolding changes, not
//     model capability. The paper's core recommendation: maintain "report and
//     control for the scaffolding version" alongside every run.
//   - Anthropic's Claude Code postmortem (2026-04-23): six weeks of "it feels
//     dumber" complaints resolved to three HARNESS changes (a reasoning-effort
//     default, a prompt-caching bug, and a system-prompt edit measured at -3%
//     quality); internal evals had missed all three.
//   - arXiv:2607.06184 (TraceProbe): trajectory-level structural features, not
//     outcome alone, localize regressions — but only if the harness that
//     produced the trajectory is recorded, otherwise diffs are meaningless.
//
// ggcode's harness has three mutable surfaces that shape every run yet were
// previously invisible in trajectories and debug exports:
//
//  1. the composed system prompt (base + dynamic layers + ratchet rules),
//  2. the write-integrity check registry (allChecks, ~191 detectors edited
//     across releases — e.g. the fc5c4aad critical-only trim),
//  3. the tool schema set (built-ins + MCP/plugins; can change mid-session).
//
// The fingerprint hashes all three and stamps each change into the debug log
// under the "harness" category (surfaced by debug log export), so a quality
// regression can be bisected to a specific scaffolding change instead of
// being misattributed to the model.

// sha256Prefix returns the first 16 hex chars (64 bits) of the SHA-256 —
// collision probability is negligible for change attribution.
func sha256Prefix(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// hashNames hashes a list of identifiers in a stable (sorted) order.
func hashNames(names []string) (string, int) {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)
	return sha256Prefix(strings.Join(sorted, "\x00")), len(sorted)
}

// integrityCheckNames lists the registered write-integrity check names.
// The registry is initialized lazily (check_registry.go); before the first
// registration the list is simply empty — and that transition is itself a
// harness change worth stamping into the log.
func integrityCheckNames() []string {
	names := make([]string, 0, len(allChecks))
	for _, c := range allChecks {
		names = append(names, c.Name)
	}
	return names
}

// harnessToolNames lists the registered tool names from an agent's registry.
func harnessToolNames(reg *tool.Registry) []string {
	if reg == nil {
		return nil
	}
	names := make([]string, 0, 32)
	for _, t := range reg.List() {
		names = append(names, t.Name())
	}
	return names
}

// HarnessFingerprint is a stable digest of the harness configuration that
// produced a given agent run. Two runs with identical fingerprints executed
// under identical scaffolding; a difference localizes exactly which surface
// moved (prompt vs. checks vs. tools).
type HarnessFingerprint struct {
	SystemPromptSHA string    `json:"system_prompt_sha"`
	SystemPromptLen int       `json:"system_prompt_len"`
	ChecksSHA       string    `json:"checks_sha"`
	ChecksCount     int       `json:"checks_count"`
	ToolsSHA        string    `json:"tools_sha"`
	ToolsCount      int       `json:"tools_count"`
	ComputedAt      time.Time `json:"computed_at"`
}

// Sum returns a compact stable identity for the whole fingerprint.
func (fp HarnessFingerprint) Sum() string {
	return fmt.Sprintf("sp=%s/%d checks=%s/%d tools=%s/%d",
		fp.SystemPromptSHA, fp.SystemPromptLen,
		fp.ChecksSHA, fp.ChecksCount,
		fp.ToolsSHA, fp.ToolsCount)
}

// ComputeHarnessFingerprint snapshots the current harness configuration.
func (a *Agent) ComputeHarnessFingerprint() HarnessFingerprint {
	a.mu.RLock()
	prompt := a.baseSystemPrompt
	reg := a.tools
	a.mu.RUnlock()

	checksSHA, checksN := hashNames(integrityCheckNames())
	toolsSHA, toolsN := hashNames(harnessToolNames(reg))
	return HarnessFingerprint{
		SystemPromptSHA: sha256Prefix(prompt),
		SystemPromptLen: len(prompt),
		ChecksSHA:       checksSHA,
		ChecksCount:     checksN,
		ToolsSHA:        toolsSHA,
		ToolsCount:      toolsN,
		ComputedAt:      time.Now(),
	}
}

// logHarnessFingerprint stamps harness changes into the debug ring. It logs
// once on first observation and again ONLY when a component changes, so a
// session with mid-flight tool registration or registry reconciliation
// produces an auditable change history instead of log spam.
func (a *Agent) logHarnessFingerprint() {
	sum := a.ComputeHarnessFingerprint().Sum()
	a.mu.Lock()
	prev := a.harnessFPLast
	a.harnessFPLast = sum
	a.mu.Unlock()

	switch {
	case prev == sum:
		// unchanged — no log
	case prev == "":
		debug.Log("harness", "harness fingerprint: %s", sum)
	default:
		debug.Log("harness", "harness fingerprint CHANGED: %s (was %s)", sum, prev)
	}
}
