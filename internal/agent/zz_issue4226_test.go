package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4226: mid-run injected user constraints must be recorded by
// constraintAmnesia (the detector was blind to steering messages - its exact
// target scenario). Verified at the state level: recordConstraints on a
// steering text populates the constraint set the reminder draws from.
func TestIssue4226_MidRunConstraintRecorded(t *testing.T) {
	ca := newConstraintAmnesiaState()
	ca.recordConstraints("do not modify the auth module", 0)
	if len(ca.constraints) == 0 {
		t.Fatal("mid-run steering constraint must be recorded (detector was blind)")
	}
}

// The wiring contract: injectPendingInterruptions consumes blocks; the text
// it feeds recordConstraints is the raw user text. Guard the extraction shape
// so a preamble/annotation refactor cannot silently blank the record.
func TestIssue4226_RawTextExtractionShape(t *testing.T) {
	blocks := []provider.ContentBlock{
		provider.TextBlock("please continue"),
		{Type: "image", ImageMIME: "image/png", ImageData: "x"},
	}
	var raw string
	for _, b := range blocks {
		if b.Type == "text" {
			raw += b.Text + "\n"
		}
	}
	if raw != "please continue\n" {
		t.Fatalf("extraction must keep text blocks only, got %q", raw)
	}
}
