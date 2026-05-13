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

// ChatView renders the chat area using a polling model.
// The background goroutine writes messages to UIState (thread-safe).
// A periodic ticker on the main goroutine reads from UIState and refreshes.
type ChatView struct {
	app    *App
	bridge *AgentBridge
	ui     *UIState

	entry     *widget.Entry
	sendBtn   *widget.Button
	cancelBtn *widget.Button
	list      *widget.List
}

func NewChatView(app *App, bridge *AgentBridge, ui *UIState) *ChatView {
	cv := &ChatView{
		app:    app,
		bridge: bridge,
		ui:     ui,
	}

	cv.entry = widget.NewMultiLineEntry()
	cv.entry.PlaceHolder = "Type a message... (Enter to send)"
	cv.entry.Wrapping = fyne.TextWrapWord
	cv.entry.SetMinRowsVisible(3)
	cv.entry.OnSubmitted = func(_ string) { cv.onSend() }

	cv.sendBtn = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), cv.onSend)
	cv.sendBtn.Importance = widget.HighImportance

	cv.cancelBtn = widget.NewButtonWithIcon("Stop", theme.CancelIcon(), func() {
		cv.bridge.Cancel()
	})
	cv.cancelBtn.Hide()

	return cv
}

func (cv *ChatView) Render() fyne.CanvasObject {
	btnStack := container.NewStack(cv.sendBtn, cv.cancelBtn)
	inputBox := container.NewBorder(nil, nil, nil, btnStack, cv.entry)

	cv.list = widget.NewList(
		func() int { return cv.ui.CountMessages() },
		func() fyne.CanvasObject { return widget.NewRichText() },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			msgs := cv.ui.TakeMessages()
			if id >= len(msgs) {
				return
			}
			rt := obj.(*widget.RichText)
			cv.renderMessage(rt, msgs[id])
		},
	)

	// Start a ticker to poll UIState for changes and refresh the list.
	// This runs on the main goroutine via widget.Refresh which is safe.
	go cv.pollRefresh()

	return container.NewBorder(
		nil,
		container.NewPadded(inputBox),
		nil, nil,
		cv.list,
	)
}

// pollRefresh periodically checks if the chat data is dirty and refreshes.
func (cv *ChatView) pollRefresh() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if cv.ui.IsDirty() {
			cv.list.Refresh()
			cv.updateButtons()
		}
	}
}

func (cv *ChatView) updateButtons() {
	working := cv.bridge.IsWorking()
	if working {
		cv.sendBtn.Disable()
		cv.cancelBtn.Show()
	} else {
		cv.sendBtn.Enable()
		cv.cancelBtn.Hide()
	}
}

func (cv *ChatView) onSend() {
	text := strings.TrimSpace(cv.entry.Text)
	if text == "" || cv.bridge.IsWorking() {
		return
	}
	cv.entry.SetText("")

	cv.ui.AppendChat(ChatMessage{
		Role:    "user",
		Content: text,
		Time:    time.Now(),
	})

	if err := cv.bridge.Send(text); err != nil {
		cv.ui.AppendChat(ChatMessage{
			Role:    "error",
			Content: err.Error(),
			Time:    time.Now(),
		})
	}
}

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
