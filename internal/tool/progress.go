package tool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Structured tool progress protocol (research round sa-217): the legacy
// ToolProgressFunc pipe carried a bare string per update, so the TUI could
// not render "42%, ~30s left" style progress, and long-task intermediate
// state had no phase semantics. ProgressEvent gives every emitter one
// shared vocabulary while the legacy pipe keeps working unchanged.

// ProgressPhase classifies where a long-running tool is in its lifecycle.
type ProgressPhase string

const (
	PhaseRunning ProgressPhase = "running" // making measurable progress
	PhaseWaiting ProgressPhase = "waiting" // blocked on an external event (job, sub-agent, timeout)
	PhaseDone    ProgressPhase = "done"    // finished successfully
	PhaseFailed  ProgressPhase = "failed"  // finished with error
)

// ProgressEvent is one structured progress update from a running tool.
// Percent/ETA are nil when the tool cannot estimate them (indeterminate).
type ProgressEvent struct {
	ToolID   string
	ToolName string
	Phase    ProgressPhase
	Message  string
	Percent  *float64
	ETA      *time.Duration
	TS       time.Time
}

// ProgressEmitter consumes structured progress events. Injected into the
// execution context by the agent loop (same injection point as the legacy
// ToolProgressKey).
type ProgressEmitter func(ev ProgressEvent)

// ProgressEmitterKey is the context key for the structured emitter.
type ProgressEmitterKey struct{}

// EmitProgress sends one structured event. Resolution order:
//  1. ProgressEmitterKey{} if present (structured consumer)
//  2. legacy ToolProgressKey{} wrapped as a Phase "running" text line
//  3. nothing (progress silently dropped, as before)
//
// This makes EmitProgress a drop-in for tools already wired to the legacy
// pipe while letting new consumers upgrade independently.
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

// Render formats the event as one human-readable line for the legacy
// string pipe and the TUI fallback path.
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

// ProgressPercent is a helper for tools that know their completion ratio.
func ProgressPercent(p float64) *float64 { return &p }

// ProgressETA is a helper for tools that can estimate remaining time.
func ProgressETA(d time.Duration) *time.Duration { return &d }
