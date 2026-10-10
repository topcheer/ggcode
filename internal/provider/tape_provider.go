package provider

// LLM response cassette (sa-44 trajectory-replay gap): GGCODE_LLM_TAPE
// records or replays every LLM response, symmetric with the tool-level
// tape (internal/agent tool_tape.go, GGCODE_TOOL_TAPE). With the tool tape
// alone, a replayed run still performs real network calls for each model
// turn - a complete trajectory could not be reproduced offline. This
// wrapper closes that loop: record captures Chat/ChatStream responses to
// a JSONL tape; replay serves them back keyed by request fingerprint, with
// FIFO fallback (same semantics as the tool tape) for runs whose message
// IDs were regenerated. A replay miss is a hard error - never a silent
// live call, matching tool_tape's fail-closed contract.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const llmTapeEnv = "GGCODE_LLM_TAPE"

// tapeEvent is the serializable projection of a StreamEvent. Error events
// store the message; replay reconstructs them with errors.New.
type tapeEvent struct {
	Type              int         `json:"t"`
	Text              string      `json:"tx,omitempty"`
	ThinkingSignature string      `json:"ts,omitempty"`
	ToolID            string      `json:"ti,omitempty"`
	ToolIndex         int         `json:"tn,omitempty"`
	ToolName          string      `json:"tm,omitempty"`
	ToolArgs          string      `json:"ta,omitempty"`
	Result            string      `json:"tr,omitempty"`
	IsError           bool        `json:"e,omitempty"`
	Usage             *TokenUsage `json:"u,omitempty"`
	Err               string      `json:"er,omitempty"`
	Truncated         bool        `json:"cut,omitempty"`
	PolicyBlocked     bool        `json:"pb,omitempty"`
	Extra             string      `json:"x,omitempty"` // reserved
}

type llmTapeEntry struct {
	Key      string        `json:"k"`
	Kind     string        `json:"kind"` // "chat" | "stream"
	Response *ChatResponse `json:"resp,omitempty"`
	Events   []tapeEvent   `json:"ev,omitempty"`
}

// TapeProvider wraps a Provider and records or replays LLM responses.
type TapeProvider struct {
	inner Provider
	mode  string // "record" | "replay"
	path  string

	mu     sync.Mutex
	file   *os.File
	cursor int             // replay position in order (strict FIFO exhaustion)
	order  []*llmTapeEntry // global FIFO fallback (replay)
}

// WrapLLMTapeFromEnv wraps p per GGCODE_LLM_TAPE=record:<path>|replay:<path>.
// Unset or malformed values return p unchanged (fail-open to live calls,
// symmetric with tool tape); replay of a missing file is a hard error.
func WrapLLMTapeFromEnv(p Provider) Provider {
	spec := os.Getenv(llmTapeEnv)
	if spec == "" || p == nil {
		return p
	}
	mode, path, ok := strings.Cut(spec, ":")
	if !ok || path == "" || (mode != "record" && mode != "replay") {
		debug.Log("provider", "invalid %s=%q, ignoring (want record:<path> or replay:<path>)", llmTapeEnv, spec)
		return p
	}
	tp := &TapeProvider{inner: p, mode: mode, path: path}
	if mode == "replay" {
		if err := tp.load(); err != nil {
			// Fail-closed at construction: a broken tape must never
			// degrade into live calls mid-replay.
			panic(fmt.Sprintf("%s replay: cannot load tape %s: %v", llmTapeEnv, path, err))
		}
	} else {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			debug.Log("provider", "%s record: cannot open %s: %v, ignoring", llmTapeEnv, path, err)
			return p
		}
		tp.file = f
	}
	debug.Log("provider", "%s active: mode=%s path=%s", llmTapeEnv, mode, path)
	return tp
}

func (t *TapeProvider) Name() string { return t.inner.Name() }

func (t *TapeProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return t.inner.CountTokens(ctx, messages) // local computation, no capture
}

// requestKey fingerprints the request: messages (IDs stripped for
// cross-run stability) + tool names. Replay matches this key first; the
// global FIFO queue is the fallback for ID-drifted runs.
func requestKey(messages []Message, tools []ToolDefinition) string {
	type stripMsg struct {
		Role    string         `json:"r"`
		Content []ContentBlock `json:"c"`
	}
	sm := make([]stripMsg, len(messages))
	for i, m := range messages {
		sm[i] = stripMsg{Role: m.Role, Content: m.Content}
	}
	tn := make([]string, len(tools))
	for i, td := range tools {
		tn[i] = td.Name
	}
	b, _ := json.Marshal([]any{sm, tn})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func (t *TapeProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	key := requestKey(messages, tools)
	if t.mode == "replay" {
		e := t.take(key)
		if e == nil || e.Kind != "chat" || e.Response == nil {
			return nil, fmt.Errorf("%s replay: no recorded chat response for key %s (tape exhausted at %s)", llmTapeEnv, key, t.path)
		}
		return e.Response, nil
	}
	resp, err := t.inner.Chat(ctx, messages, tools)
	if err != nil {
		return resp, err // errors are live signal, not replay state
	}
	t.append(&llmTapeEntry{Key: key, Kind: "chat", Response: resp})
	return resp, nil
}

func (t *TapeProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	key := requestKey(messages, tools)
	if t.mode == "replay" {
		e := t.take(key)
		if e == nil || e.Kind != "stream" {
			return nil, fmt.Errorf("%s replay: no recorded stream for key %s (tape exhausted at %s)", llmTapeEnv, key, t.path)
		}
		out := make(chan StreamEvent, len(e.Events)+1)
		go func() {
			defer close(out)
			for _, ev := range e.Events {
				out <- ev.toStreamEvent()
			}
		}()
		return out, nil
	}
	src, err := t.inner.ChatStream(ctx, messages, tools)
	if err != nil {
		return src, err
	}
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		var evs []tapeEvent
		for ev := range src {
			evs = append(evs, fromStreamEvent(ev))
			out <- ev
		}
		if len(evs) > 0 {
			t.append(&llmTapeEntry{Key: key, Kind: "stream", Events: evs})
		}
	}()
	return out, nil
}

// take pops the next entry for key, falling back to the next unconsumed
// entry in trajectory order (tool-tape semantics). A single advancing
// cursor enforces exhaustion: once the tape is fully consumed, later calls
// fail closed instead of re-serving earlier entries.
func (t *TapeProvider) take(key string) *llmTapeEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := t.cursor; i < len(t.order); i++ {
		if t.order[i].Key == key {
			t.cursor = i + 1
			return t.order[i]
		}
	}
	if t.cursor < len(t.order) {
		e := t.order[t.cursor]
		t.cursor++
		return e
	}
	return nil
}

func (t *TapeProvider) append(e *llmTapeEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	line, err := json.Marshal(e)
	if err != nil {
		debug.Log("provider", "%s record: marshal: %v", llmTapeEnv, err)
		return
	}
	if _, err := t.file.Write(append(line, '\n')); err != nil {
		debug.Log("provider", "%s record: write: %v", llmTapeEnv, err)
	}
}

func (t *TapeProvider) load() error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var e llmTapeEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return fmt.Errorf("corrupt line: %w", err)
		}
		t.order = append(t.order, &e)
	}
	return sc.Err()
}

func fromStreamEvent(ev StreamEvent) tapeEvent {
	te := tapeEvent{
		Type: int(ev.Type), Text: ev.Text,
		ThinkingSignature: ev.ThinkingSignature,
		ToolID:            ev.Tool.ID, ToolIndex: ev.Tool.Index, ToolName: ev.Tool.Name,
		ToolArgs: string(ev.Tool.Arguments),
		Result:   ev.Result, IsError: ev.IsError, Usage: ev.Usage,
		Truncated: ev.Truncated, PolicyBlocked: ev.PolicyBlocked,
	}
	if ev.Error != nil {
		te.Err = ev.Error.Error()
	}
	return te
}

func (te tapeEvent) toStreamEvent() StreamEvent {
	ev := StreamEvent{
		Type: StreamEventType(te.Type), Text: te.Text,
		ThinkingSignature: te.ThinkingSignature,
		Tool: ToolCallDelta{
			ID: te.ToolID, Index: te.ToolIndex, Name: te.ToolName,
			Arguments: json.RawMessage(te.ToolArgs),
		},
		Result: te.Result, IsError: te.IsError, Usage: te.Usage,
		Truncated: te.Truncated, PolicyBlocked: te.PolicyBlocked,
	}
	if te.Err != "" {
		ev.Error = errors.New(te.Err)
	}
	return ev
}
