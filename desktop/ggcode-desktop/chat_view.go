package main

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role      string // "user", "assistant", "system", "tool", "reasoning", "error"
	Content   string
	ToolName  string
	ToolArgs  string
	Time      time.Time
	Streaming bool
}

// ChatView renders the chat area: message list + input.
type ChatView struct {
	app       *App
	bridge    *AgentBridge
	msgs      []ChatMessage
	entry     *widget.Entry
	sendBtn   *widget.Button
	cancelBtn *widget.Button
	list      *widget.List
}

func NewChatView(app *App, bridge *AgentBridge) *ChatView {
	cv := &ChatView{
		app:    app,
		bridge: bridge,
	}

	cv.entry = widget.NewMultiLineEntry()
	cv.entry.PlaceHolder = "Type a message... (Enter to send)"
	cv.entry.Wrapping = fyne.TextWrapWord
	cv.entry.SetMinRowsVisible(3)

	// Submit on Enter (single-line behavior).
	cv.entry.OnSubmitted = func(text string) {
		cv.onSend()
	}

	cv.sendBtn = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), cv.onSend)
	cv.sendBtn.Importance = widget.HighImportance

	cv.cancelBtn = widget.NewButtonWithIcon("Stop", theme.CancelIcon(), func() {
		cv.bridge.Cancel()
	})
	cv.cancelBtn.Hide()

	return cv
}

// Render returns the fyne widget tree for this view.
func (cv *ChatView) Render() fyne.CanvasObject {
	// Input area at bottom: entry + send/cancel buttons.
	btnStack := container.NewStack(cv.sendBtn, cv.cancelBtn)
	inputBox := container.NewBorder(nil, nil, nil, btnStack, cv.entry)

	// Message list.
	cv.list = widget.NewList(
		func() int { return len(cv.msgs) },
		func() fyne.CanvasObject {
			return widget.NewRichText()
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(cv.msgs) {
				return
			}
			rt := obj.(*widget.RichText)
			cv.renderMessage(rt, cv.msgs[id])
		},
	)

	go cv.consumeEvents()

	return container.NewBorder(
		nil,
		container.NewPadded(inputBox),
		nil, nil,
		cv.list,
	)
}

func (cv *ChatView) onSend() {
	text := strings.TrimSpace(cv.entry.Text)
	if text == "" || cv.bridge.IsWorking() {
		return
	}
	cv.entry.SetText("")
	cv.sendBtn.Disable()
	cv.cancelBtn.Show()

	cv.appendMessage(ChatMessage{
		Role:    "user",
		Content: text,
		Time:    time.Now(),
	})

	if err := cv.bridge.Send(text); err != nil {
		cv.appendMessage(ChatMessage{
			Role:    "error",
			Content: fmt.Sprintf("Failed to send: %v", err),
			Time:    time.Now(),
		})
		cv.resetButtons()
	}
}

func (cv *ChatView) consumeEvents() {
	var assistantBuf strings.Builder

	for ev := range cv.bridge.Events() {
		switch ev.Type {
		case "text":
			assistantBuf.WriteString(ev.Content)
			cv.updateOrAppend(ChatMessage{
				Role:      "assistant",
				Content:   assistantBuf.String(),
				Time:      time.Now(),
				Streaming: true,
			})

		case "reasoning":
			cv.appendMessage(ChatMessage{
				Role:    "reasoning",
				Content: ev.Content,
				Time:    time.Now(),
			})

		case "tool_call":
			assistantBuf.Reset()
			args := ev.ToolArgs
			if len(args) > 100 {
				args = args[:100] + "..."
			}
			cv.appendMessage(ChatMessage{
				Role:     "tool",
				ToolName: ev.ToolName,
				ToolArgs: args,
				Content:  fmt.Sprintf("Running %s...", ev.ToolName),
				Time:     time.Now(),
			})

		case "tool_result":
			result := ev.Content
			if result == "" {
				result = "(done)"
			}
			cv.updateLastToolResult(ev.ToolName, result)

		case "system":
			assistantBuf.Reset()
			cv.appendMessage(ChatMessage{
				Role:    "system",
				Content: ev.Content,
				Time:    time.Now(),
			})

		case "error":
			assistantBuf.Reset()
			cv.appendMessage(ChatMessage{
				Role:    "error",
				Content: ev.Content,
				Time:    time.Now(),
			})
			cv.resetButtons()

		case "done":
			cv.finalizeStreaming()
			assistantBuf.Reset()
			cv.resetButtons()
		}
	}
}

// ── Message list helpers ─────────────────────────────

func (cv *ChatView) appendMessage(msg ChatMessage) {
	cv.msgs = append(cv.msgs, msg)
	cv.refreshList()
}

func (cv *ChatView) updateOrAppend(msg ChatMessage) {
	if len(cv.msgs) > 0 && cv.msgs[len(cv.msgs)-1].Role == "assistant" && cv.msgs[len(cv.msgs)-1].Streaming {
		cv.msgs[len(cv.msgs)-1].Content = msg.Content
	} else {
		cv.msgs = append(cv.msgs, msg)
	}
	cv.refreshList()
}

func (cv *ChatView) updateLastToolResult(toolName, result string) {
	for i := len(cv.msgs) - 1; i >= 0; i-- {
		if cv.msgs[i].Role == "tool" && cv.msgs[i].ToolName == toolName {
			cv.msgs[i].Content = result
			break
		}
	}
	cv.refreshList()
}

func (cv *ChatView) finalizeStreaming() {
	for i := len(cv.msgs) - 1; i >= 0; i-- {
		if cv.msgs[i].Role == "assistant" && cv.msgs[i].Streaming {
			cv.msgs[i].Streaming = false
			break
		}
	}
}

func (cv *ChatView) resetButtons() {
	cv.sendBtn.Enable()
	cv.cancelBtn.Hide()
}

func (cv *ChatView) refreshList() {
	cv.list.Refresh()
	if len(cv.msgs) > 0 {
		cv.list.ScrollToBottom()
	}
}

// ── Message rendering ────────────────────────────────

func (cv *ChatView) renderMessage(rt *widget.RichText, msg ChatMessage) {
	var segs []widget.RichTextSegment
	ts := msg.Time.Format("15:04:05")

	switch msg.Role {
	case "user":
		segs = append(segs,
			&widget.TextSegment{Style: widget.RichTextStyle{
				TextStyle: fyne.TextStyle{Bold: true},
				ColorName: theme.ColorNamePrimary,
			}, Text: fmt.Sprintf("You [%s]", ts)},
			&widget.TextSegment{Style: widget.RichTextStyle{}, Text: "\n" + msg.Content},
		)

	case "assistant":
		label := "Assistant"
		if msg.Streaming {
			label += " ..."
		}
		segs = append(segs,
			&widget.TextSegment{Style: widget.RichTextStyle{
				TextStyle: fyne.TextStyle{Bold: true},
			}, Text: fmt.Sprintf("%s [%s]", label, ts)},
			&widget.TextSegment{Style: widget.RichTextStyle{}, Text: "\n" + msg.Content},
		)

	case "tool":
		args := msg.ToolArgs
		if args != "" {
			args = " " + args
		}
		segs = append(segs,
			&widget.TextSegment{Style: widget.RichTextStyle{
				TextStyle: fyne.TextStyle{Bold: true},
				ColorName: theme.ColorNameWarning,
			}, Text: fmt.Sprintf("[Tool] %s%s", msg.ToolName, args)},
			&widget.TextSegment{Style: widget.RichTextStyle{
				ColorName: theme.ColorNameDisabled,
			}, Text: "\n" + msg.Content},
		)

	case "system":
		segs = append(segs,
			&widget.TextSegment{Style: widget.RichTextStyle{
				ColorName: theme.ColorNameDisabled,
				TextStyle: fyne.TextStyle{Italic: true},
			}, Text: msg.Content},
		)

	case "reasoning":
		segs = append(segs,
			&widget.TextSegment{Style: widget.RichTextStyle{
				ColorName: theme.ColorNamePlaceHolder,
				TextStyle: fyne.TextStyle{Italic: true},
			}, Text: fmt.Sprintf("[Thinking] %s", msg.Content)},
		)

	case "error":
		segs = append(segs,
			&widget.TextSegment{Style: widget.RichTextStyle{
				ColorName: theme.ColorNameError,
			}, Text: fmt.Sprintf("Error: %s", msg.Content)},
		)
	}

	rt.Segments = segs
	rt.Refresh()
}
