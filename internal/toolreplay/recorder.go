package toolreplay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/topcheer/ggcode/internal/tool"
)

type Recorder struct {
	inner tool.Tool
	tape  *Tape
}

func NewRecorder(inner tool.Tool, tape *Tape) *Recorder {
	return &Recorder{inner: inner, tape: tape}
}

func (r *Recorder) Name() string { return r.inner.Name() }

func (r *Recorder) Description() string { return r.inner.Description() }

func (r *Recorder) Parameters() json.RawMessage { return r.inner.Parameters() }

func (r *Recorder) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	result, err := r.inner.Execute(ctx, input)

	entry := Entry{
		ToolName:  r.inner.Name(),
		Input:     append([]byte(nil), input...),
		InputHash: HashInput(input),
		Result: Result{
			Content: result.Content,
			IsError: result.IsError,
		},
	}

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

type Replayer struct {
	inner      tool.Tool
	tape       *Tape
	strictFIFO bool
	fallback   bool
}

func NewReplayer(inner tool.Tool, tape *Tape, strictFIFO bool) *Replayer {
	return &Replayer{inner: inner, tape: tape, strictFIFO: strictFIFO}
}

func NewReplayerWithFallback(inner tool.Tool, tape *Tape, strictFIFO bool) *Replayer {
	return &Replayer{inner: inner, tape: tape, strictFIFO: strictFIFO, fallback: true}
}

func (r *Replayer) Name() string { return r.inner.Name() }

func (r *Replayer) Description() string { return r.inner.Description() }

func (r *Replayer) Parameters() json.RawMessage { return r.inner.Parameters() }

var ErrNoReplayEntry = errors.New("toolreplay: no recorded entry for this tool call and fallback is disabled")

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

func WrapRegistry(reg *tool.Registry, tape *Tape, mode string, strictFIFO bool) *tool.Registry {
	wrapped := tool.NewRegistry()
	for _, t := range reg.List() {
		switch mode {
		case "record":
			wrapped.Register(NewRecorder(t, tape))
		case "replay":
			wrapped.Register(NewReplayer(t, tape, strictFIFO))
		default:
			wrapped.Register(t)
		}
	}
	return wrapped
}
