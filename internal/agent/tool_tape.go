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

// Harness integration for deterministic record/replay of tool executions
// (the "VCR/cassette" pattern; the internal/toolreplay package).
//
// The toolreplay package existed as a fully implemented but entirely
// unwired capability: nothing outside its own package imported it. This
// file activates it at the single choke point every tool call already
// flows through (Agent.safeExecute), behind an opt-in environment variable:
//
//	GGCODE_TOOL_TAPE=record:/path/to/session.tape.json
//	    Run every tool for real, and persist each (tool, input) → result
//	    pair to the tape file after every call (atomic write, crash-safe:
//	    a tape captured up to the failure point is more valuable than none).
//
//	GGCODE_TOOL_TAPE=replay:/path/to/session.tape.json
//	    Never invoke real tools. Results are served from the tape by
//	    (tool name, canonical input hash), FIFO per repeated input. A tape
//	    miss returns an explicit error result — the real tool is NOT
//	    silently executed, so a replay can never diverge into live side
//	    effects without the operator noticing.
//
// This is the harness-engineering "inspectable execution history" lever:
// a failing session can be replayed deterministically for debugging,
// attached to a bug report, or reused as a regression fixture — without
// touching the filesystem, network, or subprocesses a second time.

// toolTapeMode selects how the tape is used during a session.
type toolTapeMode int

const (
	toolTapeOff toolTapeMode = iota
	toolTapeRecord
	toolTapeReplay
)

// toolTapeEnv is the environment variable that enables tape mode.
const toolTapeEnv = "GGCODE_TOOL_TAPE"

// toolTapeState holds the session-wide tape plus a save mutex. Tape.Lookup
// and Tape.Record are internally synchronized, but Tape.Save marshals the
// whole tape and writes through a fixed ".tmp" path followed by a rename,
// so concurrent saves from parallel tool calls could interleave corrupt
// output; the mutex serializes them.
type toolTapeState struct {
	mu   sync.Mutex
	tape *toolreplay.Tape
	mode toolTapeMode
	path string

	// Replay fidelity tracking (deterministic-replay divergence metrics).
	// replays counts tool calls served during REPLAY; misses records the
	// calls that had no recorded entry — i.e. the points where the replayed
	// trajectory diverged from the recorded one. fidelity = hits/replays.
	replays int
	hits    int
	misses  []toolTapeMiss
}

// toolTapeMiss records one replay divergence point.
type toolTapeMiss struct {
	Tool  string `json:"tool"`
	Input string `json:"input"`
}

// parseToolTapeEnv parses the GGCODE_TOOL_TAPE value. Accepted forms:
//
//	record:<path>   | RECORD:<path>   (case-insensitive prefix)
//	replay:<path>
//
// An empty/whitespace value means "off" (no error), so users can comment
// the variable out with GGCODE_TOOL_TAPE= without breaking startup.
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

// newToolTapeState resolves the tape mode from the environment. It always
// returns a non-nil state with mode off by default, so NewAgent fully
// initializes the field (TestNewAgentInitializesAllStateFields guards
// against nil state fields) and an unset env var stays inert. Any failure
// (bad value, unreadable tape) degrades to "off" with a debug log line -
// tape support must never prevent a session from starting.
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

// replayToolCall serves a recorded result for (name, args) without invoking
// the real tool. handled=true means the caller must return immediately.
// A tape miss is an explicit error result, never a silent fallback to the
// real tool: divergence from the recorded run must be visible, or the
// replay is not a replay.
func (a *Agent) replayToolCall(name string, args json.RawMessage) (tool.Result, error, bool) {
	st := a.toolTape
	if st == nil || st.mode != toolTapeReplay {
		return tool.Result{}, nil, false
	}
	st.mu.Lock()
	st.replays++
	st.mu.Unlock()
	entry, ok := st.tape.Lookup(name, args, true)
	if !ok {
		st.mu.Lock()
		st.misses = append(st.misses, toolTapeMiss{Tool: name, Input: truncateToolTapeInput(string(args))})
		st.mu.Unlock()
		debug.Log("agent", "[tool-tape] replay MISS for %s", name)
		return tool.Result{
			Content: fmt.Sprintf("[tool-replay] tape miss: no recorded entry for %s with this input in %s. The real tool was NOT executed. Adjust the input to match a recorded call, or re-record with %s=record:<path>.",
				name, st.path, toolTapeEnv),
			IsError: true,
		}, nil, true
	}
	remaining := st.tape.Len()
	debug.Log("agent", "[tool-tape] replay hit for %s (%d entries remaining)", name, remaining)
	st.mu.Lock()
	st.hits++
	st.mu.Unlock()
	res, rerr := toolResultFromEntry(entry)
	return res, rerr, true
}

// truncateToolTapeInput caps a divergent call's input for the report.
func truncateToolTapeInput(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "..."
}

// finishToolTapeReplay emits the replay fidelity report when a REPLAY-mode
// session ends. It writes <tape>.divergence.json next to the tape and logs
// a summary. Fidelity 1.0 means every replayed tool call matched a recorded
// entry; each miss is one point where the new trajectory diverged from the
// recording (counterfactual divergence), which is exactly what a developer
// replays a failing session to isolate.
func (a *Agent) finishToolTapeReplay() {
	st := a.toolTape
	if st == nil || st.mode != toolTapeReplay || st.path == "" {
		return
	}
	st.mu.Lock()
	replays, hits, misses := st.replays, st.hits, len(st.misses)
	report := struct {
		TapeFile       string         `json:"tape_file"`
		Recorded       int            `json:"recorded_entries"`
		ReplayedCalls  int            `json:"replayed_calls"`
		Hits           int            `json:"hits"`
		Misses         int            `json:"misses"`
		Fidelity       float64        `json:"fidelity"`
		DivergenceList []toolTapeMiss `json:"divergences,omitempty"`
	}{TapeFile: st.path, Recorded: st.tape.Len(), ReplayedCalls: replays, Hits: hits, Misses: misses, DivergenceList: st.misses}
	st.mu.Unlock()
	if replays > 0 {
		report.Fidelity = float64(hits) / float64(replays)
	}
	debug.Log("agent", "[tool-tape] replay fidelity %.2f (%d/%d hits, %d misses)", report.Fidelity, hits, replays, misses)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		debug.Log("agent", "[tool-tape] divergence report marshal failed: %v", err)
		return
	}
	if werr := os.WriteFile(st.path+".divergence.json", data, 0o644); werr != nil {
		debug.Log("agent", "[tool-tape] divergence report write failed: %v", werr)
	}
}

// toolResultFromEntry converts a recorded entry back into a tool result.
// The Go-level error (rare: most tool failures are Result.IsError=true)
// is reproduced as well, for full fidelity.
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

// recordToolCall persists a completed tool execution to the tape and flushes
// it to disk. Calls that ended in context cancellation are skipped — a
// cancellation result reflects the harness, not the tool, and replaying it
// would misrepresent the recorded run. Flush-after-every-call keeps the
// tape crash-safe: debugging a hang or OOM is exactly when the tail of the
// tape matters most.
func (a *Agent) recordToolCall(ctx context.Context, name string, args json.RawMessage, result tool.Result, err error) {
	st := a.toolTape
	if st == nil || st.mode != toolTapeRecord || ctx.Err() != nil {
		return
	}
	entry := toolreplay.Entry{
		ToolName:  name,
		Input:     append([]byte(nil), args...), // defensive copy
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
