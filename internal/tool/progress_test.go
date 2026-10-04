package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEmitProgressStructuredConsumer(t *testing.T) {
	var got ProgressEvent
	ctx := context.WithValue(context.Background(), ProgressEmitterKey{}, ProgressEmitter(func(ev ProgressEvent) { got = ev }))
	EmitProgress(ctx, ProgressEvent{ToolName: "wait_agent", Phase: PhaseWaiting, Message: "running tests"})
	if got.Phase != PhaseWaiting || got.Message != "running tests" {
		t.Fatalf("structured consumer did not receive event: %+v", got)
	}
	if got.TS.IsZero() {
		t.Fatal("TS must be defaulted")
	}
}

func TestEmitProgressLegacyFallback(t *testing.T) {
	var lines []string
	ctx := context.WithValue(context.Background(), ToolProgressKey{}, ToolProgressFunc(func(id, name, out string) {
		lines = append(lines, name+"|"+out)
	}))
	p := 42.5
	eta := 30 * time.Second
	EmitProgress(ctx, ProgressEvent{ToolName: "wait_agent", Phase: PhaseWaiting, Message: "building", Percent: &p, ETA: &eta})
	if len(lines) != 1 {
		t.Fatalf("legacy fallback must fire once, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "[waiting] building (42%") || !strings.Contains(lines[0], "~30s remaining") {
		t.Fatalf("rendered line missing structure: %q", lines[0])
	}
}

func TestEmitProgressNoConsumer(t *testing.T) {
	// Must not panic when nothing is injected (progress silently dropped).
	EmitProgress(context.Background(), ProgressEvent{Message: "x"})
}

func TestEmitProgressStructuredWinsOverLegacy(t *testing.T) {
	structured := false
	legacy := false
	ctx := context.WithValue(context.Background(), ToolProgressKey{}, ToolProgressFunc(func(_, _, _ string) { legacy = true }))
	ctx = context.WithValue(ctx, ProgressEmitterKey{}, ProgressEmitter(func(ev ProgressEvent) { structured = true }))
	EmitProgress(ctx, ProgressEvent{Message: "x"})
	if !structured || legacy {
		t.Fatal("structured emitter must take precedence over legacy pipe")
	}
}

func TestProgressEventRenderIndeterminate(t *testing.T) {
	ev := ProgressEvent{Phase: PhaseRunning, Message: "scanning"}
	if r := ev.Render(); r != "[running] scanning" {
		t.Fatalf("indeterminate render wrong: %q", r)
	}
}
