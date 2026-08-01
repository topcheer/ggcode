package toolreplay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/topcheer/ggcode/internal/tool"
)

// Recorder wraps a real tool.Tool. It delegates Execute to the underlying
// tool, records the result to the Tape, then returns it unchanged.
//
// During recording, the underlying tool IS executed (filesystem, network,
// subprocesses are real). Use a Recorder when you want to capture a session
// for later replay.
type Recorder struct {
	inner tool.Tool
	tape  *Tape
}

// NewRecorder creates a Recorder that records all executions to tape.
func NewRecorder(inner tool.Tool, tape *Tape) *Recorder {
	return &Recorder{inner: inner, tape: tape}
}

// Name returns the underlying tool's name.
func (r *Recorder) Name() string { return r.inner.Name() }

// Description returns the underlying tool's description.
func (r *Recorder) Description() string { return r.inner.Description() }

// Parameters returns the underlying tool's JSON schema.
func (r *Recorder) Parameters() json.RawMessage { return r.inner.Parameters() }

// Execute runs the real tool and records the result.
func (r *Recorder) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	result, err := r.inner.Execute(ctx, input)

	entry := Entry{
		ToolName:  r.inner.Name(),
		Input:     append([]byte(nil), input...), // defensive copy
		InputHash: HashInput(input),
		Result: Result{
			Content: result.Content,
			IsError: result.IsError,
		},
	}
	// Copy images (shallow — base64 strings are immutable).
	for _, img := range result.Images {
		entry.Result.Images = append(entry.Result.Images, ResultImage{
			MIME:       img.MIME,
			Base64:     img.Base64,
			Width:      img.Width,
			Height:     img.Height,
			SourcePath: img.SourcePath,
		})
	}
	if err != nil {
		entry.Err = err.Error()
	}
	r.tape.Record(entry)

	return result, err
}

// Replayer wraps a real tool.Tool but NEVER calls its Execute method.
// Instead, it looks up the recorded result from the Tape by matching the
// input hash. If no match is found, it returns an error (by default) or
// optionally falls back to the real tool.
type Replayer struct {
	inner      tool.Tool // used for metadata (Name, Parameters) only
	tape       *Tape
	strictFIFO bool // if true, each recorded entry consumed once
	fallback   bool // if true, call real tool when no match
}

// NewReplayer creates a Replayer. The inner tool provides Name(),
// Description(), and Parameters() metadata but its Execute is never called
// (unless fallback is enabled and no tape match is found).
//
// If strictFIFO is true, each recorded entry can be consumed exactly once,
// which correctly handles repeated calls with the same input returning
// different results. If false, the first matching entry is reused for all
// calls with the same input.
func NewReplayer(inner tool.Tool, tape *Tape, strictFIFO bool) *Replayer {
	return &Replayer{inner: inner, tape: tape, strictFIFO: strictFIFO}
}

// NewReplayerWithFallback creates a Replayer that falls back to the real
// tool when no tape match is found. This is useful for incremental migration:
// start with a partial tape, let unmatched calls hit the real tool.
func NewReplayerWithFallback(inner tool.Tool, tape *Tape, strictFIFO bool) *Replayer {
	return &Replayer{inner: inner, tape: tape, strictFIFO: strictFIFO, fallback: true}
}

// Name returns the underlying tool's name.
func (r *Replayer) Name() string { return r.inner.Name() }

// Description returns the underlying tool's description.
func (r *Replayer) Description() string { return r.inner.Description() }

// Parameters returns the underlying tool's JSON schema.
func (r *Replayer) Parameters() json.RawMessage { return r.inner.Parameters() }

// ErrNoReplayEntry is returned when no recorded entry matches the input
// and fallback is disabled.
var ErrNoReplayEntry = errors.New("toolreplay: no recorded entry for this tool call and fallback is disabled")

// Execute looks up the recorded result. If no match and fallback is enabled,
// delegates to the real tool.
func (r *Replayer) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	entry, ok := r.tape.Lookup(r.inner.Name(), input, r.strictFIFO)
	if !ok {
		if r.fallback {
			return r.inner.Execute(ctx, input)
		}
		return tool.Result{
			Content: fmt.Sprintf("[replay miss] no recorded entry for %s with input %s", r.inner.Name(), truncate(string(input), 200)),
			IsError: true,
		}, ErrNoReplayEntry
	}

	// Reconstruct tool.Result from the recorded entry.
	result := tool.Result{
		Content: entry.Result.Content,
		IsError: entry.Result.IsError,
	}
	for _, img := range entry.Result.Images {
		result.Images = append(result.Images, tool.ResultImage{
			MIME:       img.MIME,
			Base64:     img.Base64,
			Width:      img.Width,
			Height:     img.Height,
			SourcePath: img.SourcePath,
		})
	}

	var err error
	if entry.Err != "" {
		err = errors.New(entry.Err)
	}
	return result, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// WrapRegistry wraps every tool in a Registry with a Recorder or Replayer.
//
// In record mode (mode == "record"), all tools are wrapped with Recorder.
// In replay mode (mode == "replay"), all tools are wrapped with Replayer.
//
// This is the integration point for enabling record/replay across the entire
// agent tool set. The caller controls mode via environment variable or flag.
func WrapRegistry(reg *tool.Registry, tape *Tape, mode string, strictFIFO bool) *tool.Registry {
	wrapped := tool.NewRegistry()
	for _, t := range reg.List() {
		switch mode {
		case "record":
			wrapped.Register(NewRecorder(t, tape))
		case "replay":
			wrapped.Register(NewReplayer(t, tape, strictFIFO))
		default:
			wrapped.Register(t) // pass-through
		}
	}
	return wrapped
}
