package agent

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
	interventionHintThreshold = 3
	interventionRecencyWindow = 14 * 24 * time.Hour
	interventionHintCooldown  = 24 * time.Hour
	interventionLedgerFile    = "interventions.json"
)

type interventionEntry struct {
	Tool      string `json:"tool"`
	Iteration int    `json:"iter"`
	Ts        int64  `json:"ts"`
}

type interventionLedgerData struct {
	Entries  []interventionEntry `json:"entries"`
	HintedAt map[string]int64    `json:"hinted_at,omitempty"`
}

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

func (l *interventionLedger) clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = interventionLedgerData{HintedAt: map[string]int64{}}
	l.saveLocked()
}

func (a *Agent) recordIntervention(iteration int) {
	a.mu.RLock()
	tool := a.lastExecutedTool
	a.mu.RUnlock()
	a.interventionLedger.record(tool, iteration)
}

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

func (a *Agent) ClearInterventions() {
	a.interventionLedger.clear()
}

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
