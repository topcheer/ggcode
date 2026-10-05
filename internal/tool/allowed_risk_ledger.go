package tool

import (
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

// r28 (DreamGuard-inspired, arXiv:2608.05695): Allowed-Risk Accumulation
// Guard - the dual of the SecurityLedger.
//
// Frontier claim: single-step guardrails have a blind spot for long-horizon
// risk - "individually benign-looking actions can gradually drift the agent
// toward hazardous states". The SecurityLedger (sa-216) records DENIALS
// (probe behavior), but a denied action cannot accumulate danger; the
// actions that DO accumulate it are the ALLOWED ones. In auto/bypass mode a
// session of individually-permitted medium-danger commands (each chmod,
// each small external transfer, each non-critical file removal passes the
// gate) can push the system toward a hazardous state with zero accounting
// anywhere. This ledger scores every ALLOWED dangerous-classified command
// and escalates once the session-level accumulation crosses a threshold.
//
// Nil-safe (unwired = inert), memory-capped, deterministic, zero LLM cost.

// AllowedRiskEvent is one permitted-but-risky command execution.
type AllowedRiskEvent struct {
	Time    time.Time
	Command string
	Level   permission.DangerLevel
	Reason  string
}

// Score weights: high-danger commands are rarer and individually closer to
// the hazard line, so three of them (or a mix totalling 8) escalates;
// medium-danger commands need a sustained pattern (5+ or score 8) - normal
// trial-and-error of one or two medium hits never trips.
const (
	riskScoreMedium   = 1.0
	riskScoreHigh     = 3.0
	riskScoreCritical = 4.0

	// riskScoreThreshold: cumulative session score that escalates. Tuned to
	// require ~8 medium or ~3 high (or a mix) - past that, the pattern is a
	// gradual drift, not noise.
	riskScoreThreshold = 8.0

	// riskClassThreshold: same-class repetitions that escalate even below
	// the score threshold (the DreamGuard "drift" shape: many small same-
	// kind steps, e.g. chmod-ing files one by one).
	riskClassThreshold = 5

	// maxRiskEvents bounds memory (mirrors SecurityLedger).
	maxRiskEvents = 200

	// maxRiskWarnings caps escalations per session: after the agent has
	// been told twice, further nagging is noise.
	maxRiskWarnings = 2
)

// AllowedRiskLedger accumulates risk scores of allowed dangerous-classified
// commands. Wire it where SecLedger is wired; nil receiver is inert.
type AllowedRiskLedger struct {
	mu             sync.Mutex
	events         []AllowedRiskEvent
	score          float64
	warned         int
	lastFiredScore float64
	detect         *permission.DangerousDetector
}

// NewAllowedRiskLedger builds a ledger with its own DangerousDetector
// (permission.NewDangerousDetector, zero-config pattern table).
func NewAllowedRiskLedger() *AllowedRiskLedger {
	return &AllowedRiskLedger{detect: permission.NewDangerousDetector()}
}

// Accumulate records one ALLOWED command. Only commands the detector
// classifies as dangerous (medium+) score; everything else returns
// immediately (the common path stays cheap). Called on the gate-passed
// branch - the dual position of SecurityLedger.Record on the blocked one.
func (l *AllowedRiskLedger) Accumulate(command string) {
	if l == nil {
		return
	}
	chk := l.detect.Check(command)
	if chk.Level < permission.DangerMedium {
		return // allowed AND not risky: no accumulation interest
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) >= maxRiskEvents {
		l.events = l.events[len(l.events)/2:]
	}
	l.events = append(l.events, AllowedRiskEvent{
		Time:    time.Now(),
		Command: command,
		Level:   chk.Level,
		Reason:  chk.Reason,
	})
	switch chk.Level {
	case permission.DangerCritical:
		l.score += riskScoreCritical
	case permission.DangerHigh:
		l.score += riskScoreHigh
	default:
		l.score += riskScoreMedium
	}
}

// Escalation returns a human/agent-readable warning when the session's
// allowed-risk accumulation crosses a threshold, or "". Fires at most
// maxRiskWarnings times; once fired it stays "due" until drained, so
// callers may poll after every Accumulate.
func (l *AllowedRiskLedger) Escalation() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.warned >= maxRiskWarnings || len(l.events) == 0 {
		return ""
	}
	// Same-class drift: the most repeated level's count.
	byLevel := map[permission.DangerLevel]int{}
	for _, e := range l.events {
		byLevel[e.Level]++
	}
	var worstLevel permission.DangerLevel
	var worstN int
	for lv, n := range byLevel {
		if n > worstN {
			worstLevel, worstN = lv, n
		}
	}
	if l.score < riskScoreThreshold && worstN < riskClassThreshold {
		return ""
	}
	// Fire once per CROSSING (not per poll): after a warning, another
	// full threshold's worth of fresh risk must accumulate before the next
	// one - polling callers see one warning per crossing, capped at
	// maxRiskWarnings per session.
	if l.warned > 0 && l.score < l.lastFiredScore+riskScoreThreshold {
		return ""
	}
	l.warned++
	l.lastFiredScore = l.score
	// Newest-first recent sample of the ACCUMULATED (not all) commands.
	var recent []string
	for i := len(l.events) - 1; i >= 0 && len(recent) < 3; i-- {
		recent = append(recent, l.events[i].Command)
	}
	levelWord := "medium-risk"
	if worstLevel == permission.DangerHigh {
		levelWord = "high-risk"
	}
	return fmt.Sprintf(
		"RISK ACCUMULATION: %d allowed dangerous-classified commands this session (score %.1f, threshold %.1f). Each was individually permitted, but together they form a drift pattern: %s - %s. Recent: %s. Pause and reassess whether the cumulative effect of these actions was intended, or switch to explicit user approval for further ones.",
		len(l.events), l.score, riskScoreThreshold, levelWord, l.events[len(l.events)-1].Reason, clipCommandList(recent))
}

// Score returns the current cumulative score (tests/observability).
func (l *AllowedRiskLedger) Score() float64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.score
}

// Events returns a copy of recorded risk events (newest last).
func (l *AllowedRiskLedger) Events() []AllowedRiskEvent {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AllowedRiskEvent, len(l.events))
	copy(out, l.events)
	return out
}
