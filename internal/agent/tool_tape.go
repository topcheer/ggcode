package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/toolreplay"
)

type toolTapeMode int

const (
	toolTapeOff toolTapeMode = iota
	toolTapeRecord
	toolTapeReplay
)

const toolTapeEnv = "GGCODE_TOOL_TAPE"

type toolTapeState struct {
	mu   sync.Mutex
	tape *toolreplay.Tape
	mode toolTapeMode
	path string
}

func parseToolTapeEnv(raw string) (toolTapeMode, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return toolTapeOff, "", nil
	}
	prefix, rest, found := strings.Cut(trimmed, ":")
	if !found {
		return toolTapeOff, "", fmt.Errorf("%s must be 'record:<path>' or 'replay:<path>', got %q", toolTapeEnv, trimmed)
	}
	path := strings.TrimSpace(rest)
	if path == "" {
		return toolTapeOff, "", fmt.Errorf("%s mode %q requires a tape file path", toolTapeEnv, prefix)
	}
	switch strings.ToLower(prefix) {
	case "record":
		return toolTapeRecord, path, nil
	case "replay":
		return toolTapeReplay, path, nil
	default:
		return toolTapeOff, "", fmt.Errorf("%s must be 'record:<path>' or 'replay:<path>', got %q", toolTapeEnv, trimmed)
	}
}

func newToolTapeState() *toolTapeState {
	mode, path, err := parseToolTapeEnv(os.Getenv(toolTapeEnv))
	if err != nil {
		debug.Log("agent", "[tool-tape] disabled: %v", err)
		return &toolTapeState{mode: toolTapeOff}
	}
	if mode == toolTapeOff {
		return &toolTapeState{mode: toolTapeOff}
	}
	st := &toolTapeState{mode: mode, path: path}
	switch mode {
	case toolTapeRecord:
		st.tape = toolreplay.NewTape()
		debug.Log("agent", "[tool-tape] RECORD mode, tape file: %s", path)
	case toolTapeReplay:
		tape, loadErr := toolreplay.LoadTape(path)
		if loadErr != nil {
			debug.Log("agent", "[tool-tape] REPLAY mode disabled, load failed: %v", loadErr)
			return &toolTapeState{mode: toolTapeOff}
		}
		st.tape = tape
		debug.Log("agent", "[tool-tape] REPLAY mode, %d recorded entries from %s", tape.Len(), path)
	}
	return st
}

func (a *Agent) replayToolCall(name string, args json.RawMessage) (tool.Result, error, bool) {
	st := a.toolTape
	if st == nil || st.mode != toolTapeReplay {
		return tool.Result{}, nil, false
	}
	entry, ok := st.tape.Lookup(name, args, true)
	if !ok {
		debug.Log("agent", "[tool-tape] replay MISS for %s", name)
		return tool.Result{
			Content: fmt.Sprintf("[tool-replay] tape miss: no recorded entry for %s with this input in %s. The real tool was NOT executed. Adjust the input to match a recorded call, or re-record with %s=record:<path>.",
				name, st.path, toolTapeEnv),
			IsError: true,
		}, nil, true
	}
	remaining := st.tape.Len()
	debug.Log("agent", "[tool-tape] replay hit for %s (%d entries remaining)", name, remaining)
	res, rerr := toolResultFromEntry(entry)
	return res, rerr, true
}

func toolResultFromEntry(e toolreplay.Entry) (tool.Result, error) {
	result := tool.Result{Content: e.Result.Content, IsError: e.Result.IsError}
	if len(e.Result.Images) > 0 {
		result.Images = make([]tool.ResultImage, 0, len(e.Result.Images))
		for _, img := range e.Result.Images {
			result.Images = append(result.Images, tool.ResultImage{
				MIME:       img.MIME,
				Base64:     img.Base64,
				Width:      img.Width,
				Height:     img.Height,
				SourcePath: img.SourcePath,
			})
		}
	}
	var err error
	if e.Err != "" {
		err = fmt.Errorf("%s", e.Err)
	}
	return result, err
}

func (a *Agent) recordToolCall(ctx context.Context, name string, args json.RawMessage, result tool.Result, err error) {
	st := a.toolTape
	if st == nil || st.mode != toolTapeRecord || ctx.Err() != nil {
		return
	}
	entry := toolreplay.Entry{
		ToolName:  name,
		Input:     append([]byte(nil), args...),
		InputHash: toolreplay.HashInput(args),
		Result: toolreplay.Result{
			Content: result.Content,
			IsError: result.IsError,
		},
	}
	for _, img := range result.Images {
		entry.Result.Images = append(entry.Result.Images, toolreplay.ResultImage{
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
	st.mu.Lock()
	defer st.mu.Unlock()
	st.tape.Record(entry)
	if saveErr := st.tape.Save(st.path); saveErr != nil {
		debug.Log("agent", "[tool-tape] save failed: %v", saveErr)
	}
}
