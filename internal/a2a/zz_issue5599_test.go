package a2a

import (
	"strings"
	"testing"
)

// #5599: FormatSnapshot must not panic when the Busy face is nil
// (pre-phase-2b peer stub / unwired source / nil-snap handler branch).
func TestFormatSnapshotNilBusyNoPanic(t *testing.T) {
	out := FormatSnapshot("peer", "ws", &RuntimeSnapshotV2{Schema: "v2", Mode: "full"})
	if !strings.Contains(out, "face not available") {
		t.Fatalf("nil Busy should degrade to 'face not available', got: %q", out)
	}
	if strings.Contains(out, "panic") {
		t.Fatalf("panic leaked into output: %q", out)
	}
}

func TestFormatSnapshotBusyFaceRendered(t *testing.T) {
	out := FormatSnapshot("peer", "ws", &RuntimeSnapshotV2{
		Schema: "v2", Mode: "full",
		Busy: &RuntimeBusy{Busy: true, InFlightTool: "read_file", BusySince: "2026-11-13T00:00:00Z"},
	})
	if !strings.Contains(out, "Busy: true") || !strings.Contains(out, "in-flight=read_file") {
		t.Fatalf("busy face should render, got: %q", out)
	}
}
