package agent

// intervention_ledger.go -- Intervention Point Ledger (proactive defer, V1).
//
// Research basis:
//   - CMU ML Blog (2026-04) "When Should AI Step Aside?": agents should learn
//     not only to act but to DEFER - anticipating when a human wants to take
//     over. CowCorpus (400 real human-agent sessions, 4200+ interleaved
//     actions with step-level intervention annotations) shows intervention
//     precursors are learnable, and that the human-agent boundary should be
//     adaptive (user-specific), not fixed rules.
//   - Learning-to-defer formalization: Madras/Pitassi/Zemel "Predict
//     Responsibly: Improving Fairness and Accuracy by Learning to Defer".
//
// Gap in this codebase (audited r395): mid-run steering (#1472/r229) provides
// the intervention CHANNEL (can the user interject) but nothing learns the
// intervention TIMING. ApprovalMemory learns auto-approve (opposite
// direction, permission layer). No persistent record of where users actually
// interrupted exists, so a user who has taken over at the same kind of step
// three times gets no adaptation on the fourth.
//
// V1 scope (deliberately conservative - stats, not a model):
//   - record(): when a mid-run interruption lands, persist the tool the agent
//     had most recently executed + iteration depth. Tool NAMES and counts
//     only - never arguments, never content (privacy).
//   - hint(): before/around executing a tool whose category historically
//     drew >=3 interruptions (within the recency window), append a
//     NON-BLOCKING note to that tool's result asking the agent to state its
//     intent before the next such action. This converts the supervision cost
//     from "must interrupt" (high) to "glance and see" (low) - the defer
//     posture without an annoying ask-first gate. V1 deliberately does NOT
//     ask the user a question: in a TUI the user is already watching, and a
//     blocking prompt on a guessed pattern would cost more trust than it
//     buys.
//   - Stored under <workspace>/.ggcode/interventions.json, rolling window,
//     workspace-scoped. `/interventions clear` wipes the profile.
//
// Protocol safety: the hint is appended to the tool RESULT content (same
// pattern as effect_ledger.go and command_cache.go annotations); no message
// is inserted between tool_calls and tool_results.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	interventionMaxEntries    = 200
	interventionHintThreshold = 3 // distinct interruptions on the same tool
	interventionRecencyWindow = 14 * 24 * time.Hour
	interventionHintCooldown  = 24 * time.Hour // one hint per tool per day
	interventionLedgerFile    = "interventions.json"
)

// interventionEntry is one recorded user takeover.
type interventionEntry struct {
	Tool      string `json:"tool"` // tool the agent had just executed (or "" if none yet)
	Iteration int    `json:"iter"` // loop iteration the run had reached
	Ts        int64  `json:"ts"`
}

// interventionLedgerData is the on-disk JSON structure.
type interventionLedgerData struct {
	Entries []interventionEntry `json:"entries"`
	// HintedAt tracks last-hint wall time per tool to enforce the cooldown.
	HintedAt map[string]int64 `json:"hinted_at,omitempty"`
}

// interventionLedger records user interruptions and surfaces defer hints.
type interventionLedger struct {
	mu         sync.Mutex
	workingDir string
	data       interventionLedgerData
}

func newInterventionLedger(workingDir string) *interventionLedger {
	l := &interventionLedger{workingDir: workingDir}
	l.load()
	return l
}

func (l *interventionLedger) path() string {
	return filepath.Join(l.workingDir, ".ggcode", interventionLedgerFile)
}

func (l *interventionLedger) load() {
	raw, err := os.ReadFile(l.path())
	if err != nil {
		return
	}
	var d interventionLedgerData
	if json.Unmarshal(raw, &d) == nil {
		l.data = d
	}
}

func (l *interventionLedger) saveLocked() {
	if l.data.HintedAt == nil {
		l.data.HintedAt = map[string]int64{}
	}
	raw, err := json.MarshalIndent(l.data, "", " ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(l.path()), 0o755)
	tmp := l.path() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, l.path())
	}
}

// record persists one intervention. Never blocks: ledger failures are
// silently dropped - the hint is an optimization, not an invariant.
func (l *interventionLedger) record(tool string, iteration int) {
	if l == nil {
		return
	}
	tool = sanitizeInterventionTool(tool)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data.Entries = append(l.data.Entries, interventionEntry{
		Tool:      tool,
		Iteration: iteration,
		Ts:        time.Now().Unix(),
	})
	if excess := len(l.data.Entries) - interventionMaxEntries; excess > 0 {
		l.data.Entries = l.data.Entries[excess:]
	}
	l.saveLocked()
}

// recentCount returns how many interventions on this tool fall inside the
// recency window.
func (l *interventionLedger) recentCount(tool string) int {
	tool = sanitizeInterventionTool(tool)
	cutoff := time.Now().Add(-interventionRecencyWindow).Unix()
	n := 0
	for _, e := range l.data.Entries {
		if e.Tool == tool && e.Ts >= cutoff {
			n++
		}
	}
	return n
}

// hint returns a non-blocking defer note when the tool being executed has
// historically drawn repeated takeovers and no hint fired recently. Empty
// string means no hint. Callers append the note to the tool result.
func (l *interventionLedger) hint(tool string) string {
	if l == nil {
		return ""
	}
	tool = sanitizeInterventionTool(tool)
	if tool == "" {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.recentCount(tool) < interventionHintThreshold {
		return ""
	}
	now := time.Now()
	if t, ok := l.data.HintedAt[tool]; ok && now.Unix()-t < int64(interventionHintCooldown.Seconds()) {
		return ""
	}
	if l.data.HintedAt == nil {
		l.data.HintedAt = map[string]int64{}
	}
	l.data.HintedAt[tool] = now.Unix()
	l.saveLocked()
	return "[intervention history] The user has taken over at this kind of step (" + tool +
		") " + interventionItoa(l.recentCount(tool)) + " times recently. Before the next " + tool +
		" action, state in one sentence what you are about to do and why, so the user can veto cheaply instead of interrupting."
}

// clear wipes the profile (backing /interventions clear).
func (l *interventionLedger) clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = interventionLedgerData{HintedAt: map[string]int64{}}
	l.saveLocked()
}

// recordIntervention attributes a user takeover to the tool the agent had
// most recently executed (r395). Called from the agent loop when
// injectPendingInterruptions() reports that mid-run guidance landed.
func (a *Agent) recordIntervention(iteration int) {
	a.mu.RLock()
	tool := a.lastExecutedTool
	a.mu.RUnlock()
	a.interventionLedger.record(tool, iteration)
}

// sanitizeInterventionTool normalizes a tool name for the profile: strip
// whitespace, lowercase, cap length. Arguments are never recorded.
func sanitizeInterventionTool(tool string) string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if len(tool) > 64 {
		tool = tool[:64]
	}
	return tool
}

func interventionItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ClearInterventions wipes the takeover history (/interventions clear).
func (a *Agent) ClearInterventions() {
	a.interventionLedger.clear()
}

// InterventionSummary renders the takeover history for the user.
func (a *Agent) InterventionSummary() string {
	a.interventionLedger.mu.Lock()
	defer a.interventionLedger.mu.Unlock()
	if len(a.interventionLedger.data.Entries) == 0 {
		return "No recorded user interventions. Takeover points are recorded when you send guidance mid-run (r395 intervention ledger)."
	}
	counts := map[string]int{}
	cutoff := time.Now().Add(-interventionRecencyWindow).Unix()
	for _, e := range a.interventionLedger.data.Entries {
		if e.Ts >= cutoff {
			counts[e.Tool]++
		}
	}
	var sb strings.Builder
	sb.WriteString("User takeover history (last 14 days): ")
	first := true
	for tool, n := range counts {
		if !first {
			sb.WriteString(", ")
		}
		first = false
		label := tool
		if label == "" {
			label = "(start of run)"
		}
		sb.WriteString(label)
		sb.WriteString(":")
		sb.WriteString(interventionItoa(n))
	}
	return sb.String()
}
