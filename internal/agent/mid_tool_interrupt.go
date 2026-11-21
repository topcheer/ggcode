package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Mid-tool interruption checkpoints (#5842).
//
// injectPendingInterruptions used to run only at LLM iteration boundaries,
// so while a single long tool executed (30-minute run_command, generate,
// wait_agent) every user interjection sat in the channel-side buffer:
// several long tools back to back meant interjections arrived tens of
// minutes late - "completely unresponsive" to the user.
//
// The tool progress path now drains the interruption handler while the
// tool still executes:
//   - text/image interjections are staged and injected into the
//     conversation at the next safe boundary, right after the pending
//     tool_result message, where consecutive user messages are
//     protocol-safe for every provider (no fold ambiguity);
//   - a retraction-stop ("stop", "cancel") cancels the executing tool at
//     the next progress event and aborts the remaining tool batch,
//     instead of waiting for the tool to finish on its own;
//   - a system stream event acknowledges the interjection the moment the
//     agent perceives it, closing the perception gap.
//
// Threading rules: the checkpoint runs on whatever goroutine emits tool
// progress (typically a command-output reader). It only touches state that
// is individually synchronized (onInterrupt handler, midTool.mu) and never
// mutates the conversation directly - context injection happens on the
// agent goroutine at the flush sites.

// midToolCheckInterval bounds how often the progress path polls the
// interruption handler. An empty drain is a cheap locked queue check, but
// chatty tools can emit hundreds of progress events per second.
const midToolCheckInterval = 250 * time.Millisecond

type stagedInterruption struct {
	blocks []provider.ContentBlock
	kind   InterruptionKind
}

type midToolCancelEntry struct {
	cancel context.CancelFunc
}

type midToolInterruptState struct {
	mu         sync.Mutex
	staged     []stagedInterruption
	stopReq    bool
	cancels    []*midToolCancelEntry
	lastCheck  time.Time
	emitStream func(provider.StreamEvent)
}

// setMidToolStreamEmitter records the active run's onEvent so checkpoint
// acknowledgments can reach the UI while a tool is still executing.
func (a *Agent) setMidToolStreamEmitter(fn func(provider.StreamEvent)) {
	a.midTool.mu.Lock()
	a.midTool.emitStream = fn
	a.midTool.mu.Unlock()
}

// midToolInterruptCheckpoint drains the interruption handler from inside a
// tool's progress stream (#5842). Safe to call from any goroutine.
func (a *Agent) midToolInterruptCheckpoint(toolName string) {
	a.midTool.mu.Lock()
	if time.Since(a.midTool.lastCheck) < midToolCheckInterval {
		a.midTool.mu.Unlock()
		return
	}
	a.midTool.lastCheck = time.Now()
	emit := a.midTool.emitStream
	a.midTool.mu.Unlock()

	a.mu.RLock()
	fn := a.onInterrupt
	a.mu.RUnlock()
	if fn == nil {
		return
	}
	blocks := fn()
	if !interruptionBlocksNonBlank(blocks) {
		return
	}
	kind := classifyInterruption(firstTextBlock(blocks))
	debug.Log("agent", "mid-tool interruption checkpoint (tool=%s): user interjection perceived (kind=%s)", toolName, kind)

	a.midTool.mu.Lock()
	a.midTool.staged = append(a.midTool.staged, stagedInterruption{blocks: blocks, kind: kind})
	cancels := append([]*midToolCancelEntry(nil), a.midTool.cancels...)
	if kind == InterruptionRetractStop {
		a.midTool.stopReq = true
	}
	a.midTool.mu.Unlock()

	if kind == InterruptionRetractStop {
		debug.Log("agent", "mid-tool retraction-stop (tool=%s): cancelling %d executing tool(s)", toolName, len(cancels))
		for _, c := range cancels {
			c.cancel()
		}
	}

	if emit != nil {
		text := fmt.Sprintf("[mid-run] User interjection received while %s is still running - queued into the conversation, it will guide the next step.", toolName)
		if kind == InterruptionRetractStop {
			text = fmt.Sprintf("[mid-run] User retraction received while %s was running - cancelling the running tool and stopping the run.", toolName)
		}
		emit(provider.StreamEvent{Type: provider.StreamEventSystem, Text: text})
	}
}

// registerMidToolCancel tracks the cancel func of a tool currently
// executing so a mid-tool retraction-stop can abort it. Tools may nest
// (delegation wrappers), hence a stack.
func (a *Agent) registerMidToolCancel(cancel context.CancelFunc) *midToolCancelEntry {
	e := &midToolCancelEntry{cancel: cancel}
	a.midTool.mu.Lock()
	a.midTool.cancels = append(a.midTool.cancels, e)
	a.midTool.mu.Unlock()
	return e
}

func (a *Agent) unregisterMidToolCancel(e *midToolCancelEntry) {
	a.midTool.mu.Lock()
	for i, c := range a.midTool.cancels {
		if c == e {
			a.midTool.cancels = append(a.midTool.cancels[:i], a.midTool.cancels[i+1:]...)
			break
		}
	}
	a.midTool.mu.Unlock()
}

// takeMidToolStop reports and clears a pending mid-tool retraction-stop.
func (a *Agent) takeMidToolStop() bool {
	a.midTool.mu.Lock()
	v := a.midTool.stopReq
	a.midTool.stopReq = false
	a.midTool.mu.Unlock()
	return v
}

// flushStagedMidToolInterruptions moves staged mid-tool interjections into
// the conversation. Runs on the agent goroutine (iteration boundary or the
// mid-batch retraction abort), after the pending tool_result message has
// landed.
func (a *Agent) flushStagedMidToolInterruptions(iter int) (bool, InterruptionKind) {
	a.midTool.mu.Lock()
	staged := a.midTool.staged
	a.midTool.staged = nil
	a.midTool.mu.Unlock()
	if len(staged) == 0 {
		return false, InterruptionAddition
	}
	for _, s := range staged {
		a.addInterruptionMessage(s.blocks, s.kind, iter)
	}
	return true, staged[len(staged)-1].kind
}

// addInterruptionMessage injects one interjection with its preamble into
// the conversation. Agent goroutine only.
func (a *Agent) addInterruptionMessage(blocks []provider.ContentBlock, kind InterruptionKind, iter int) {
	content := []provider.ContentBlock{{
		Type: "text",
		Text: interruptionPreamble(kind),
	}}
	content = append(content, blocks...)
	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: content,
	})
	a.constraintAmnesia.recordConstraints(firstTextBlock(blocks), iter)
}

// interruptionBlocksNonBlank reports whether a drained interjection carries
// any content: text blocks must have non-blank text, any other block type
// (image, ...) counts.
func interruptionBlocksNonBlank(blocks []provider.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) == "" {
			continue
		}
		return true
	}
	return false
}
