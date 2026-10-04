package tool

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// SecurityLedger (research round sa-216, sandbox escape detection): the
// PREVENTION side of agent safety (command gate, OS sandbox, path sandbox)
// is mature, but the DETECTION side was absent - denied commands lived only
// in the debug ring buffer, invisible to users and unaggregated. An agent
// (or a prompt injection riding it) probing the sandbox with repeated
// variations of a blocked command is a security signal the runtime should
// surface instead of silently looping.
//
// The ledger records every denial (gate block, gate ask in bypass mode,
// sandbox EPERM) and derives escalation notices when the same rule kind
// denies repeatedly - the behavioral fingerprint of sandbox probing.
type SecurityLedger struct {
	mu     sync.Mutex
	events []DenialEvent
}

// DenialEvent is one recorded denial of an agent-driven command attempt.
type DenialEvent struct {
	Time     time.Time
	Command  string
	RuleKind string // gate rule kind ("dangerous", "network", ...) or "sandbox"
	Denier   string // "gate" or "sandbox"
}

// denialEscalationThreshold: the same denial source firing this many times
// in one session is probing behavior, not accidents. Small enough to catch
// a real probe within a run, large enough that normal agent trial-and-error
// (one blocked attempt, then self-correction) never trips it.
const denialEscalationThreshold = 3

// maxLedgerEvents caps the ledger so a runaway loop cannot grow it unbounded;
// escalation counting reads the full window before eviction, so the cap only
// bounds memory, not detection coverage.
const maxLedgerEvents = 200

// Record appends a denial event. Nil receiver is safe (ledger not wired).
func (l *SecurityLedger) Record(denier, ruleKind, command string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) >= maxLedgerEvents {
		l.events = l.events[len(l.events)/2:]
	}
	l.events = append(l.events, DenialEvent{
		Time:     time.Now(),
		Command:  command,
		RuleKind: ruleKind,
		Denier:   denier,
	})
}

// Escalation returns a human/agent-readable warning when any single denial
// source (denier+ruleKind) has fired denialEscalationThreshold or more
// times, or "" when the session's denials look like normal trial-and-error.
// The warning names the most-fired source and lists its recent commands so
// the agent (and the user reading the transcript) sees the probe pattern.
func (l *SecurityLedger) Escalation() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	type key struct{ denier, kind string }
	counts := map[key]int{}
	lastCmd := map[key]string{}
	first := map[key]int{}
	for i, e := range l.events {
		k := key{e.Denier, e.RuleKind}
		counts[k]++
		lastCmd[k] = e.Command
		if _, seen := first[k]; !seen {
			first[k] = i
		}
	}
	var worst key
	worstN := 0
	for k, n := range counts {
		if n > worstN {
			worst, worstN = k, n
		}
	}
	if worstN < denialEscalationThreshold {
		return ""
	}
	var recent []string
	shown := 0
	for _, e := range l.events {
		if e.Denier == worst.denier && e.RuleKind == worst.kind {
			recent = append(recent, e.Command)
			shown++
			if shown == 3 {
				break
			}
		}
	}
	return fmt.Sprintf(
		"SECURITY ESCALATION: the command %s has denied %d attempts in this session (rule %q via %s). Recent denied commands: %s. Repeated attempts to bypass a denial are a sandbox-probing pattern - stop varying the command, explain what you need to the user, or choose a permitted approach.",
		"gate/sandbox", worstN, worst.kind, worst.denier, clipCommandList(recent))
}

// Events returns a copy of the recorded denials (newest last) for /security
// display and tests.
func (l *SecurityLedger) Events() []DenialEvent {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DenialEvent, len(l.events))
	copy(out, l.events)
	return out
}

func clipCommandList(cmds []string) string {
	var parts []string
	for _, c := range cmds {
		c = strings.TrimSpace(c)
		if len(c) > 60 {
			c = c[:57] + "..."
		}
		parts = append(parts, fmt.Sprintf("%q", c))
	}
	return strings.Join(parts, ", ")
}
