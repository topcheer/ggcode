package tool

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type SecurityLedger struct {
	mu     sync.Mutex
	events []DenialEvent
}

type DenialEvent struct {
	Time     time.Time
	Command  string
	RuleKind string
	Denier   string
}

const denialEscalationThreshold = 3

const maxLedgerEvents = 200

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
	for i := len(l.events) - 1; i >= 0 && len(recent) < 3; i-- {
		e := l.events[i]
		if e.Denier == worst.denier && e.RuleKind == worst.kind {
			recent = append(recent, e.Command)
		}
	}
	return fmt.Sprintf(

		"SECURITY ESCALATION: %q was denied %d times this session by the %s (rule %q via %s). Recent denied commands: %s. Repeated attempts to bypass a denial are a sandbox-probing pattern - stop varying the command, explain what you need to the user, or choose a permitted approach.",
		lastCmd[worst], worstN, worst.denier, worst.kind, worst.denier, clipCommandList(recent))
}

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
