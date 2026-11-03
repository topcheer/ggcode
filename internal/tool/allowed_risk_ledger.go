package tool

import (
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

type AllowedRiskEvent struct {
	Time    time.Time
	Command string
	Level   permission.DangerLevel
	Reason  string
}

const (
	riskScoreMedium   = 1.0
	riskScoreHigh     = 3.0
	riskScoreCritical = 4.0

	riskScoreThreshold = 8.0

	riskClassThreshold = 5

	maxRiskEvents = 200

	maxRiskWarnings = 2
)

type AllowedRiskLedger struct {
	mu             sync.Mutex
	events         []AllowedRiskEvent
	score          float64
	warned         int
	lastFiredScore float64
	detect         *permission.DangerousDetector
}

func NewAllowedRiskLedger() *AllowedRiskLedger {
	return &AllowedRiskLedger{detect: permission.NewDangerousDetector()}
}

func (l *AllowedRiskLedger) Accumulate(command string) {
	if l == nil {
		return
	}
	chk := l.detect.Check(command)
	if chk.Level < permission.DangerMedium {
		return
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

func (l *AllowedRiskLedger) Escalation() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.warned >= maxRiskWarnings || len(l.events) == 0 {
		return ""
	}

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

	if l.warned > 0 && l.score < l.lastFiredScore+riskScoreThreshold {
		return ""
	}
	l.warned++
	l.lastFiredScore = l.score
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

func (l *AllowedRiskLedger) Score() float64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.score
}

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
