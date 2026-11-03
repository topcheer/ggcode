package webui

import (
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

type TUIAgent interface {
	Messages() []provider.Message
}

type WebchatMessageSender interface {
	SendWebchatMessage(text string, images []provider.ContentBlock)
}

type tuiBridgeSubscriber struct {
	ch   chan provider.StreamEvent
	done chan struct{}
}

type TUIChatBridge struct {
	agent  TUIAgent
	sender WebchatMessageSender
	subMu  sync.RWMutex
	subs   []*tuiBridgeSubscriber
}

func NewTUIChatBridge(agent TUIAgent, sender WebchatMessageSender) *TUIChatBridge {
	return &TUIChatBridge{agent: agent, sender: sender}
}

func (b *TUIChatBridge) Messages() []provider.Message {
	return b.agent.Messages()
}

func (b *TUIChatBridge) SendUserMessage(content []provider.ContentBlock) {
	text := extractText(content)

	var images []provider.ContentBlock
	for _, blk := range content {
		if blk.Type == "image" {
			images = append(images, blk)
		}
	}
	if (text == "" && len(images) == 0) || b.sender == nil {
		return
	}
	debug.Log("tui-bridge", "routing webchat message to TUI: %s (+%d images)", truncateStr(text, 80), len(images))
	b.sender.SendWebchatMessage(text, images)
}

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

		var done chan struct{}
		b.subMu.Lock()
		if b.subs[idx] != nil {
			close(b.subs[idx].ch)
			done = b.subs[idx].done
			b.subs[idx] = nil
		}
		b.subMu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				debug.Log("tui-bridge", "subscriber drain timed out after unsubscription")
			}
		}
	}
}

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
