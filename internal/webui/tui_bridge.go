package webui

import (
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

// TUIAgent is the subset of agent.Agent needed by TUIChatBridge.
type TUIAgent interface {
	Messages() []provider.Message
}

// WebchatMessageSender injects a webchat user message into the TUI event loop.
// Implemented by a closure that calls program.Send(webchatUserMsg{...}).
type WebchatMessageSender interface {
	SendWebchatMessage(text string)
}

// tuiBridgeSubscriber wraps a callback with an async channel.
type tuiBridgeSubscriber struct {
	ch   chan provider.StreamEvent
	done chan struct{}
}

// TUIChatBridge implements ChatBridge for TUI mode.
// In TUI mode, the agent is driven by the bubbletea program.
// Webchat messages are injected into the TUI event loop (not directly
// into the agent), so they go through the normal submit flow.
// Agent events are broadcast via Subscribe (wired in TUI's stream callback).
type TUIChatBridge struct {
	agent  TUIAgent
	sender WebchatMessageSender
	subMu  sync.RWMutex
	subs   []*tuiBridgeSubscriber
}

// NewTUIChatBridge creates a ChatBridge for TUI mode.
func NewTUIChatBridge(agent TUIAgent, sender WebchatMessageSender) *TUIChatBridge {
	return &TUIChatBridge{agent: agent, sender: sender}
}

// Messages returns the current agent conversation history.
func (b *TUIChatBridge) Messages() []provider.Message {
	return b.agent.Messages()
}

// SendUserMessage routes a webchat message through the TUI event loop.
// The message appears to the agent as if the user typed it in the terminal.
// This avoids any concurrency issues with the agent — the TUI handles
// queuing, interruption, and submission just like a keyboard input.
func (b *TUIChatBridge) SendUserMessage(content []provider.ContentBlock) {
	text := extractText(content)
	if text == "" || b.sender == nil {
		return
	}
	debug.Log("tui-bridge", "routing webchat message to TUI: %s", truncateStr(text, 80))
	b.sender.SendWebchatMessage(text)
}

// Subscribe registers a callback for agent streaming events.
// In TUI mode, events are broadcast from the TUI's stream callback
// via BroadcastEvent (called in internal/tui/submit.go).
func (b *TUIChatBridge) Subscribe(fn func(provider.StreamEvent)) func() {
	sub := &tuiBridgeSubscriber{
		ch:   make(chan provider.StreamEvent, 256),
		done: make(chan struct{}),
	}
	safego.Go("webui.tuiBridge.subscriber", func() {
		defer close(sub.done)
		for event := range sub.ch {
			func() {
				defer safego.Recover("webui.tuiBridge.subscriberCallback")
				fn(event)
			}()
		}
	})

	b.subMu.Lock()
	defer b.subMu.Unlock()
	b.subs = append(b.subs, sub)
	idx := len(b.subs) - 1
	return func() {
		b.subMu.Lock()
		defer b.subMu.Unlock()
		if b.subs[idx] != nil {
			close(b.subs[idx].ch)
			<-b.subs[idx].done
			b.subs[idx] = nil
		}
	}
}

// BroadcastEvent sends an event to all subscribers.
// Called from the TUI's agent stream callback to forward events to webchat.
func (b *TUIChatBridge) BroadcastEvent(event provider.StreamEvent) {
	b.subMu.RLock()
	defer b.subMu.RUnlock()
	for _, sub := range b.subs {
		if sub != nil {
			select {
			case sub.ch <- event:
			default:
				debug.Log("tui-bridge", "subscriber channel full, dropping event")
			}
		}
	}
}

func extractText(blocks []provider.ContentBlock) string {
	var text string
	for _, b := range blocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return text
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
