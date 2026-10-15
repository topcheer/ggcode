package provider

// #4146 regression tests: the record-mode tape forwarding goroutine
// (tape_provider.go) and the fault-injection ChatStream forwarding goroutine
// (fault_inject.go) must both exit when the consumer abandons the stream and
// ctx is cancelled, instead of parking forever on a bare `out <- ev` against
// an unbuffered channel. Exit is observable as out being closed (close only
// runs via defer after the goroutine returns). The tape recorder must also
// still append its stream entry on abandon - the pre-fix leak silently
// dropped it.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitStreamClosed(t *testing.T, ch <-chan StreamEvent) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
			// Leftover in-flight events are fine; keep draining.
		case <-deadline:
			t.Fatal("forwarding goroutine leaked: out not closed after ctx cancel")
		}
	}
}

func TestFaultInjectStreamExitsOnConsumerAbandon(t *testing.T) {
	// fakeTapeProvider streams 3 events into a buffered channel and closes
	// it immediately, so the forwarding goroutine is guaranteed to reach its
	// send on the unbuffered out with no reader left.
	fp := &FaultProvider{inner: &fakeTapeProvider{}, plan: []faultDirective{{kind: "omit-content", call: 1}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := fp.ChatStream(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Consume nothing: simulate consumer abandonment (panic containment
	// recovery leaves no reader on the stream channel).
	cancel()
	waitStreamClosed(t, out)
}

func TestTapeRecordStreamExitsAndRecordsOnConsumerAbandon(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "t.jsonl")
	t.Setenv("GGCODE_LLM_TAPE", "record:"+tape)
	rp := WrapLLMTapeFromEnv(&fakeTapeProvider{})
	msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "q"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := rp.ChatStream(ctx, msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Read one event, then abandon the stream.
	select {
	case _, ok := <-out:
		if !ok {
			t.Fatal("expected at least one event before abandoning")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no first event within deadline")
	}
	cancel()
	waitStreamClosed(t, out)

	// The abandon path must still have appended the stream entry: the
	// pre-fix parked goroutine never reached append, silently losing this
	// turn's events from the tape.
	raw, err := os.ReadFile(tape)
	if err != nil {
		t.Fatal(err)
	}
	streams := 0
	for _, line := range splitTapeLines(raw) {
		var e llmTapeEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Kind == "stream" && len(e.Events) >= 1 {
			streams++
		}
	}
	if streams == 0 {
		t.Fatal("abandoned stream must still be recorded on the tape")
	}
}

func splitTapeLines(raw []byte) []string {
	var lines []string
	start := 0
	for i, b := range raw {
		if b == '\n' {
			if i > start {
				lines = append(lines, string(raw[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, string(raw[start:]))
	}
	return lines
}
