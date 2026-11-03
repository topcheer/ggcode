package provider

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
	"github.com/topcheer/ggcode/internal/safego"
)

const llmTapeEnv = "GGCODE_LLM_TAPE"

type tapeEvent struct {
	Type              int           `json:"t"`
	Text              string        `json:"tx,omitempty"`
	ThinkingSignature string        `json:"ts,omitempty"`
	ToolID            string        `json:"ti,omitempty"`
	ToolIndex         int           `json:"tn,omitempty"`
	ToolName          string        `json:"tm,omitempty"`
	ToolArgs          string        `json:"ta,omitempty"`
	Result            string        `json:"tr,omitempty"`
	IsError           bool          `json:"e,omitempty"`
	Usage             *TokenUsage   `json:"u,omitempty"`
	Err               string        `json:"er,omitempty"`
	Truncated         bool          `json:"cut,omitempty"`
	PolicyBlocked     bool          `json:"pb,omitempty"`
	Confidence        *float64      `json:"cf,omitempty"`
	Block             *ContentBlock `json:"blk,omitempty"`
	ToolServerTool    bool          `json:"tsv,omitempty"`
	ToolThoughtSig    string        `json:"tts,omitempty"`
	ToolCallerRaw     string        `json:"tcr,omitempty"`
	Extra             string        `json:"x,omitempty"`
}

type llmTapeEntry struct {
	Key      string        `json:"k"`
	Kind     string        `json:"kind"`
	Response *ChatResponse `json:"resp,omitempty"`
	Events   []tapeEvent   `json:"ev,omitempty"`
}

type TapeProvider struct {
	inner Provider
	mode  string
	path  string

	mu     sync.Mutex
	file   *os.File
	cursor int
	order  []*llmTapeEntry
}

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
	return t.inner.CountTokens(ctx, messages)
}

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
		return resp, err
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
			defer safego.Recover("tape.replayStream")
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
		defer safego.Recover("tape.recordStream")
		defer close(out)
		var evs []tapeEvent

		drainSrc := func() {
			go safego.Run("tape.recordStream.drain", func() {
				for range src {
				}
			})
		}
		defer drainSrc()
		for ev := range src {
			evs = append(evs, fromStreamEvent(ev))
			select {
			case out <- ev:
			case <-ctx.Done():

				t.append(&llmTapeEntry{Key: key, Kind: "stream", Events: evs})
				return
			}
		}

		t.append(&llmTapeEntry{Key: key, Kind: "stream", Events: evs})
	}()
	return out, nil
}

func (t *TapeProvider) take(key string) *llmTapeEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := t.cursor; i < len(t.order); i++ {
		if t.order[i].Key == key {
			matched := t.order[i]
			t.order = append(t.order[:i], t.order[i+1:]...)

			return matched
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

		Confidence:     ev.Confidence,
		ToolServerTool: ev.Tool.ServerTool,
		ToolThoughtSig: string(ev.Tool.ThoughtSignature),
		ToolCallerRaw:  string(ev.Tool.Caller),
	}
	if ev.Block.Type != "" || len(ev.Block.Raw) > 0 {
		blk := ev.Block
		te.Block = &blk
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

			ServerTool:       te.ToolServerTool,
			ThoughtSignature: []byte(te.ToolThoughtSig),
			Caller:           json.RawMessage(te.ToolCallerRaw),
		},
		Confidence: te.Confidence,
		Result:     te.Result, IsError: te.IsError, Usage: te.Usage,
		Truncated: te.Truncated, PolicyBlocked: te.PolicyBlocked,
	}
	if te.Err != "" {
		ev.Error = errors.New(te.Err)
	}
	if te.Block != nil {
		ev.Block = *te.Block
	}
	return ev
}
