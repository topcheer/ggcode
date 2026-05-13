package main

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ChatView renders the chat area. Background goroutines write to UIState.
// pollRefresh reads via fyne.Do() on the main Fyne goroutine.
type ChatView struct {
	bridge *AgentBridge
	ui     *UIState

	entry     *widget.Entry
	sendBtn   *widget.Button
	cancelBtn *widget.Button
	scroll    *container.Scroll
	vbox      *fyne.Container
}

func NewChatView(bridge *AgentBridge, ui *UIState) *ChatView {
	cv := &ChatView{bridge: bridge, ui: ui}

	cv.entry = widget.NewMultiLineEntry()
	cv.entry.PlaceHolder = "Message ggcode... (Enter to send)"
	cv.entry.Wrapping = fyne.TextWrapWord
	cv.entry.SetMinRowsVisible(2)

	cv.sendBtn = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), cv.onSend)
	cv.sendBtn.Importance = widget.HighImportance

	cv.cancelBtn = widget.NewButtonWithIcon("Stop", theme.CancelIcon(), func() {
		cv.bridge.Cancel()
	})
	cv.cancelBtn.Importance = widget.DangerImportance
	cv.cancelBtn.Hide()

	return cv
}

func (cv *ChatView) Render() fyne.CanvasObject {
	// Input bar at bottom.
	btnRow := container.NewHBox(cv.cancelBtn, cv.sendBtn)
	inputBar := container.NewBorder(nil, nil, nil, btnRow, cv.entry)

	// Message area.
	cv.vbox = container.NewVBox()
	cv.scroll = container.NewVScroll(cv.vbox)

	go cv.pollRefresh()

	return container.NewBorder(nil, container.NewPadded(inputBar), nil, nil, cv.scroll)
}

func (cv *ChatView) pollRefresh() {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		dirty := cv.ui.IsDirty()
		working := cv.bridge.IsWorking()
		if !dirty && !working {
			continue
		}
		fyne.Do(func() {
			if dirty {
				cv.rebuildMessages()
			}
			cv.updateButtons(working)
		})
	}
}

func (cv *ChatView) updateButtons(working bool) {
	if working {
		cv.sendBtn.Hide()
		cv.cancelBtn.Show()
	} else {
		cv.sendBtn.Show()
		cv.cancelBtn.Hide()
		cv.sendBtn.Enable()
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

// rebuildMessages recreates all message widgets from the UIState snapshot.
func (cv *ChatView) rebuildMessages() {
	msgs := cv.ui.TakeMessages()
	objects := make([]fyne.CanvasObject, 0, len(msgs))

	for i := range msgs {
		w := cv.buildMessageWidget(&msgs[i])
		if w != nil {
			objects = append(objects, w)
		}
	}

	cv.vbox.Objects = objects
	cv.vbox.Refresh()
	cv.scroll.ScrollToBottom()
}

// ── Message widgets ──────────────────────────────────

func (cv *ChatView) buildMessageWidget(msg *ChatMessage) fyne.CanvasObject {
	switch msg.Role {
	case "user":
		return cv.userBubble(msg)
	case "assistant":
		return cv.assistantBubble(msg)
	case "tool":
		return cv.toolItem(msg)
	case "system":
		return cv.systemNotice(msg)
	case "reasoning":
		return cv.reasoningNotice(msg)
	case "error":
		return cv.errorNotice(msg)
	}
	return nil
}

// userBubble — right-aligned, accent background.
func (cv *ChatView) userBubble(msg *ChatMessage) fyne.CanvasObject {
	content := widget.NewRichText(&widget.TextSegment{
		Style: widget.RichTextStyle{},
		Text:  msg.Content,
	})
	content.Wrapping = fyne.TextWrapWord

	inner := container.NewVBox(
		container.NewPadded(content),
	)

	// Use a card with no title for visual grouping.
	return container.NewPadded(widget.NewCard("", "", inner))
}

// assistantBubble — left-aligned, subtle.
func (cv *ChatView) assistantBubble(msg *ChatMessage) fyne.CanvasObject {
	text := msg.Content
	if text == "" && msg.Streaming {
		text = "..."
	}

	content := widget.NewRichText(&widget.TextSegment{
		Style: widget.RichTextStyle{},
		Text:  text,
	})
	content.Wrapping = fyne.TextWrapWord

	inner := container.NewVBox(
		container.NewPadded(content),
	)

	return container.NewPadded(widget.NewCard("", "", inner))
}

// toolItem — compact, collapsible accordion.
func (cv *ChatView) toolItem(msg *ChatMessage) fyne.CanvasObject {
	// Display: description > "toolName args"
	displayTitle := msg.ToolDesc
	if displayTitle == "" || displayTitle == msg.ToolName {
		displayTitle = msg.ToolName
	}
	if msg.ToolArgs != "" {
		displayTitle = fmt.Sprintf("%s  %s", displayTitle, msg.ToolArgs)
	}

	// Status indicator.
	status := "done"
	if msg.Content == "" {
		status = "running..."
	}

	header := fmt.Sprintf("%s  (%s)", displayTitle, status)

	// Truncate result for display.
	result := msg.Content
	if len(result) > 1000 {
		result = result[:1000] + "\n...(truncated)"
	}

	var detail fyne.CanvasObject
	if result == "" {
		spinner := canvas.NewText("running...", theme.DisabledColor())
		spinner.TextStyle = fyne.TextStyle{Italic: true}
		detail = container.NewPadded(spinner)
	} else {
		resultLabel := widget.NewLabel(result)
		resultLabel.Wrapping = fyne.TextWrapWord
		resultLabel.TextStyle = fyne.TextStyle{Monospace: true}
		detail = container.NewPadded(resultLabel)
	}

	acc := widget.NewAccordion(widget.NewAccordionItem(header, detail))
	acc.MultiOpen = true

	// Make it compact.
	return container.New(layout.NewGridWrapLayout(fyne.NewSize(0, 0)),
		container.NewPadded(acc),
	)
}

// systemNotice — subtle, centered.
func (cv *ChatView) systemNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText(msg.Content, theme.DisabledColor())
	text.TextStyle = fyne.TextStyle{Italic: true}
	text.TextSize = theme.Size(theme.SizeNameCaptionText)
	text.Alignment = fyne.TextAlignCenter
	return container.NewPadded(text)
}

// reasoningNotice — subtle, italic.
func (cv *ChatView) reasoningNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText("Thinking: "+msg.Content, theme.DisabledColor())
	text.TextStyle = fyne.TextStyle{Italic: true}
	text.TextSize = theme.Size(theme.SizeNameCaptionText)
	return container.NewPadded(text)
}

// errorNotice — red, prominent.
func (cv *ChatView) errorNotice(msg *ChatMessage) fyne.CanvasObject {
	text := canvas.NewText("Error: "+msg.Content, theme.ErrorColor())
	text.TextSize = theme.TextSize()
	return container.NewPadded(text)
}
