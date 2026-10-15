package provider

// LLM fault injection layer (sa-65 agent-chaos gap, AgentChaos ASE 2026 /
// arXiv:2608.06790): GGCODE_LLM_FAULT deterministically perturbs LLM
// responses at the shared provider boundary with an explicit fault
// taxonomy — crash / omission / value, acting on content and tool-call
// fields. Resilience mechanisms EXISTING (retry, fallback chain, error
// classification, JSON repair, truncation detection) is not the same as
// resilience being VERIFIED; this layer turns those defenses into
// testable properties. Composes with the tape cassette: pair
// GGCODE_LLM_TAPE=replay:<good tape> with GGCODE_LLM_FAULT for offline,
// zero-network chaos tests.
//
// Spec grammar: comma-separated directives "<kind>@<call>", 1-based call
// index in arrival order across Chat and ChatStream. Kinds:
//
//	crash        — provider-level failure before the call is made
//	omit-tool    — drop tool_use blocks / tool-call events
//	omit-content — drop text blocks / text events
//	value-args   — corrupt tool arguments into invalid JSON
//	value-text   — truncate text mid-content (half, rune-safe)
//
// Faults are recorded in Fired() only when observable in agent input
// (AgentChaos "verify fault triggered"): an omit-tool on a response with
// no tool calls did not happen as far as the agent can tell.

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
	call int // 1-based
}

// FaultProvider wraps a Provider and injects faults per GGCODE_LLM_FAULT.
type FaultProvider struct {
	inner Provider
	plan  []faultDirective

	mu    sync.Mutex
	calls int
	fired []string
}

// WrapLLMFaultFromEnv wraps p per GGCODE_LLM_FAULT. Unset, empty, or
// entirely-invalid specs return p unchanged (fail-open, symmetric with
// the tape); individual invalid directives are skipped with a log.
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

// beginCall advances the shared call counter (Chat and ChatStream are
// numbered together) and returns the directive scheduled for this call.
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

// fire records an OBSERVED fault (verify-triggered semantics).
func (f *FaultProvider) fire(d faultDirective) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fired = append(f.fired, fmt.Sprintf("%s@%d", d.kind, d.call))
	debug.Log("provider", "%s fired: %s@%d", llmFaultEnv, d.kind, d.call)
}

// Fired returns the directives whose effect was observable in agent
// input, in firing order. Chaos tests assert on this instead of
// trusting that a planned fault actually landed.
func (f *FaultProvider) Fired() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.fired))
	copy(out, f.fired)
	return out
}

func (f *FaultProvider) Name() string { return f.inner.Name() }

// InnerProvider exposes the wrapped provider (composition/introspection
// in tests: assert fault layer sits outside the tape layer).
func (f *FaultProvider) InnerProvider() Provider { return f.inner }

func (f *FaultProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return f.inner.CountTokens(ctx, messages) // local computation, no injection
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
		// #4146: abandoning consumers (see tape_provider recordStream) parked
		// this goroutine on the bare `out <- nev` forever. ctx-guarded send +
		// deferred src drain, matching the fallback.go #602/#2570 standard.
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
				// The fault was (at least partially) observed before the
				// abandon; record it as consumed so call numbering stays
				// aligned with what the harness actually saw.
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

// perturbChatResponse returns a copied, perturbed response. The original
// (which may be a shared replay-tape entry) is never mutated.
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

// perturbStreamEvent applies one fault kind to a single stream event.
// Returns (event, drop, mutated); the input event is never modified.
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

// corruptJSON truncates a JSON payload to an invalid prefix (half the
// bytes, rune-boundary safe, never closed). Empty input gets a broken
// sentinel so value-args still corrupts argument-less tool calls.
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

// halveRuneSafe cuts s to half its length without splitting a rune.
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
