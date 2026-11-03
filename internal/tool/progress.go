package tool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type ProgressPhase string

const (
	PhaseRunning ProgressPhase = "running"
	PhaseWaiting ProgressPhase = "waiting"
	PhaseDone    ProgressPhase = "done"
	PhaseFailed  ProgressPhase = "failed"
)

type ProgressEvent struct {
	ToolID   string
	ToolName string
	Phase    ProgressPhase
	Message  string
	Percent  *float64
	ETA      *time.Duration
	TS       time.Time
}

type ProgressEmitter func(ev ProgressEvent)

type ProgressEmitterKey struct{}

func EmitProgress(ctx context.Context, ev ProgressEvent) {
	if ctx == nil {
		return
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now()
	}
	if em, ok := ctx.Value(ProgressEmitterKey{}).(ProgressEmitter); ok && em != nil {
		em(ev)
		return
	}
	if fn, ok := ctx.Value(ToolProgressKey{}).(ToolProgressFunc); ok && fn != nil {
		fn(ev.ToolID, ev.ToolName, ev.Render())
	}
}

func (ev ProgressEvent) Render() string {
	var b strings.Builder
	if ev.Phase != "" {
		b.WriteString(fmt.Sprintf("[%s] ", ev.Phase))
	}
	b.WriteString(ev.Message)
	if ev.Percent != nil {
		b.WriteString(fmt.Sprintf(" (%.0f%%)", *ev.Percent))
	}
	if ev.ETA != nil {
		b.WriteString(fmt.Sprintf(" ~%s remaining", ev.ETA.Truncate(time.Second)))
	}
	return b.String()
}

func ProgressPercent(p float64) *float64 { return &p }

func ProgressETA(d time.Duration) *time.Duration { return &d }
