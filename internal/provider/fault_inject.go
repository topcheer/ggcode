package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const llmFaultEnv = "GGCODE_LLM_FAULT"

var faultKinds = map[string]bool{
	"crash": true, "omit-tool": true, "omit-content": true,
	"value-args": true, "value-text": true,
}

type faultDirective struct {
	kind string
	call int
}

type FaultProvider struct {
	inner Provider
	plan  []faultDirective

	mu    sync.Mutex
	calls int
	fired []string
}

func WrapLLMFaultFromEnv(p Provider) Provider {
	spec := os.Getenv(llmFaultEnv)
	if spec == "" || p == nil {
		return p
	}
	var plan []faultDirective
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kind, idx, ok := strings.Cut(part, "@")
		if !ok || !faultKinds[kind] {
			debug.Log("provider", "%s: skipping invalid directive %q", llmFaultEnv, part)
			continue
		}
		n, err := strconv.Atoi(idx)
		if err != nil || n < 1 {
			debug.Log("provider", "%s: skipping invalid call index in %q", llmFaultEnv, part)
			continue
		}
		plan = append(plan, faultDirective{kind: kind, call: n})
	}
	if len(plan) == 0 {
		debug.Log("provider", "%s=%q has no valid directive, ignoring", llmFaultEnv, spec)
		return p
	}
	debug.Log("provider", "%s active: %d directive(s): %s", llmFaultEnv, len(plan), spec)
	return &FaultProvider{inner: p, plan: plan}
}

func (f *FaultProvider) beginCall() (faultDirective, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	for _, d := range f.plan {
		if d.call == f.calls {
			return d, true
		}
	}
	return faultDirective{}, false
}

func (f *FaultProvider) fire(d faultDirective) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fired = append(f.fired, fmt.Sprintf("%s@%d", d.kind, d.call))
	debug.Log("provider", "%s fired: %s@%d", llmFaultEnv, d.kind, d.call)
}

func (f *FaultProvider) Fired() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.fired))
	copy(out, f.fired)
	return out
}

func (f *FaultProvider) Name() string { return f.inner.Name() }

func (f *FaultProvider) InnerProvider() Provider { return f.inner }

func (f *FaultProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return f.inner.CountTokens(ctx, messages)
}

func (f *FaultProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	d, hit := f.beginCall()
	if hit && d.kind == "crash" {
		f.fire(d)
		return nil, fmt.Errorf("%s: simulated provider crash at call %d", llmFaultEnv, d.call)
	}
	resp, err := f.inner.Chat(ctx, messages, tools)
	if err != nil || !hit {
		return resp, err
	}
	perturbed, applied := perturbChatResponse(resp, d.kind)
	if applied {
		f.fire(d)
	}
	return perturbed, nil
}

func (f *FaultProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	d, hit := f.beginCall()
	if hit && d.kind == "crash" {
		f.fire(d)
		return nil, fmt.Errorf("%s: simulated provider crash at call %d", llmFaultEnv, d.call)
	}
	src, err := f.inner.ChatStream(ctx, messages, tools)
	if err != nil {
		return src, err
	}
	if !hit {
		return src, nil
	}
	out := make(chan StreamEvent)
	go func() {
		defer safego.Recover("fault.injectStream")
		defer close(out)
		applied := false

		defer func() {
			go safego.Run("fault.injectStream.drain", func() {
				for range src {
				}
			})
		}()
		for ev := range src {
			nev, drop, mutated := perturbStreamEvent(ev, d.kind)
			if drop {
				applied = true
				continue
			}
			if mutated {
				applied = true
			}
			select {
			case out <- nev:
			case <-ctx.Done():

				if applied {
					f.fire(d)
				}
				return
			}
		}
		if applied {
			f.fire(d)
		}
	}()
	return out, nil
}

func perturbChatResponse(resp *ChatResponse, kind string) (*ChatResponse, bool) {
	cp := *resp
	msg := resp.Message
	content := make([]ContentBlock, 0, len(msg.Content))
	applied := false
	for _, b := range msg.Content {
		switch kind {
		case "omit-tool":
			if b.Type == "tool_use" {
				applied = true
				continue
			}
		case "omit-content":
			if b.Type == "text" {
				applied = true
				continue
			}
		case "value-args":
			if b.Type == "tool_use" {
				b.Input = corruptJSON(b.Input)
				applied = true
			}
		case "value-text":
			if b.Type == "text" && b.Text != "" {
				b.Text = halveRuneSafe(b.Text)
				applied = true
			}
		}
		content = append(content, b)
	}
	if !applied {
		return resp, false
	}
	msg.Content = content
	cp.Message = msg
	return &cp, true
}

func isToolEvent(ev StreamEvent) bool {
	return ev.Tool.Name != "" || ev.Tool.ID != "" || len(ev.Tool.Arguments) > 0
}

func perturbStreamEvent(ev StreamEvent, kind string) (StreamEvent, bool, bool) {
	tool := isToolEvent(ev)
	text := ev.Text != "" && !tool
	switch kind {
	case "omit-tool":
		if tool {
			return ev, true, false
		}
	case "omit-content":
		if text {
			return ev, true, false
		}
	case "value-args":
		if tool && len(ev.Tool.Arguments) > 0 {
			ev.Tool.Arguments = corruptJSON(ev.Tool.Arguments)
			return ev, false, true
		}
	case "value-text":
		if text {
			ev.Text = halveRuneSafe(ev.Text)
			return ev, false, true
		}
	}
	return ev, false, false
}

func corruptJSON(raw json.RawMessage) json.RawMessage {
	s := string(raw)
	if s == "" {
		return json.RawMessage(`{"broken":`)
	}
	half := halveRuneSafe(s)
	if half == "" {
		half = s[:1]
	}
	return json.RawMessage(half)
}

func halveRuneSafe(s string) string {
	half := len(s) / 2
	for half > 0 && !utf8.RuneStart(s[half]) {
		half--
	}
	if half <= 0 {
		return ""
	}
	return s[:half]
}
