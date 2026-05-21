package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/provider"

	"github.com/topcheer/ggcode/desktop/markdownx"
)

// newMD creates a MarkdownWidget with the given text.
func newMD(text string) *markdownx.MarkdownWidget {
	w := markdownx.NewMarkdownWidget()
	w.SetMarkdown(text)
	return w
}

// ── sendEntry ────────────────────────────────────────

type sendEntry struct {
	widget.Entry
	onSend          func()
	busy            bool
	pendingText     string
	pendingImage    *provider.ContentBlock
	onImageAttached func()
	history         []string
	historyIdx      int
	historyDraft    string // current unsent text saved when navigating history // called when image is attached (show preview bar)
}

func newSendEntry() *sendEntry {
	e := &sendEntry{}
	e.MultiLine = true
	e.ExtendBaseWidget(e)
	return e
}

func (e *sendEntry) KeyDown(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		if e.busy {
			return
		}
		if e.isShiftHeld() {
			e.Entry.KeyDown(key)
			return
		}
		text := strings.TrimSpace(e.Text)
		e.SetText("")
		if text != "" && e.onSend != nil {
			e.appendHistory(text)
			e.pendingText = text
			e.onSend()
		}
		return
	case fyne.KeyUp:
		e.navigateHistory(-1)
		return
	case fyne.KeyDown:
		e.navigateHistory(1)
		return
	default:
		e.Entry.KeyDown(key)
	}
}

// navigateHistory moves through message history.
func (e *sendEntry) navigateHistory(dir int) {
	if len(e.history) == 0 {
		return
	}
	if e.historyIdx == len(e.history) {
		e.historyDraft = e.Text
	}
	newIdx := e.historyIdx + dir
	if newIdx < 0 || newIdx > len(e.history) {
		return
	}
	e.historyIdx = newIdx
	if newIdx == len(e.history) {
		e.SetText(e.historyDraft)
	} else {
		e.SetText(e.history[newIdx])
	}
}

// appendHistory adds a sent message to history.
func (e *sendEntry) appendHistory(text string) {
	if text == "" {
		return
	}
	if len(e.history) > 0 && e.history[len(e.history)-1] == text {
		e.historyIdx = len(e.history)
		return
	}
	e.history = append(e.history, text)
	e.historyIdx = len(e.history)
}

// loadHistory sets history from session user messages.
func (e *sendEntry) loadHistory(msgs []string) {
	e.history = msgs
	e.historyIdx = len(e.history)
	e.historyDraft = ""
}

// TypedKey intercepts the Return/Enter key to prevent the base Entry
// from inserting a newline. Fyne calls TypedKey AFTER KeyDown, and
// the base Entry.typedKeyReturn inserts "\n" if we don't block it here.
func (e *sendEntry) TypedKey(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		if e.busy || !e.isShiftHeld() {
			// Already handled in KeyDown — swallow the event.
			return
		}
	}
	e.Entry.TypedKey(key)
}

func (e *sendEntry) isShiftHeld() bool {
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		m := d.CurrentKeyModifiers()
		return m&fyne.KeyModifierShift != 0
	}
	return false
}

func (e *sendEntry) attachImage(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mime := "image/png"
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") {
		mime = "image/jpeg"
	} else if strings.HasSuffix(lower, ".gif") {
		mime = "image/gif"
	} else if strings.HasSuffix(lower, ".webp") {
		mime = "image/webp"
	}
	block := provider.ImageBlock(mime, base64.StdEncoding.EncodeToString(data))
	e.pendingImage = &block
	return nil
}

func (e *sendEntry) clearImage() {
	e.pendingImage = nil
}

// TypedShortcut handles Ctrl+V to detect image paste.
func (e *sendEntry) TypedShortcut(s fyne.Shortcut) {
	if _, ok := s.(*fyne.ShortcutPaste); ok {
		// Run clipboard check in goroutine to avoid blocking UI.
		go func() {
			if e.tryPasteImageFromClipboard() {
				return
			}
		}()
	}
	e.Entry.TypedShortcut(s)
}

// tryPasteImageFromClipboard reads image from system clipboard (cross-platform).
func (e *sendEntry) tryPasteImageFromClipboard() bool {
	tmpFile := os.TempDir() + string(os.PathSeparator) + "ggcode-clipboard-paste.png"
	os.Remove(tmpFile)

	var ok bool
	switch runtime.GOOS {
	case "darwin":
		ok = pasteImageDarwin(tmpFile)
	case "linux":
		ok = pasteImageLinux(tmpFile)
	case "windows":
		ok = pasteImageWindows(tmpFile)
	default:
		return false
	}
	if !ok {
		return false
	}
	info, err := os.Stat(tmpFile)
	if err != nil || info.Size() == 0 {
		return false
	}
	if err := e.attachImage(tmpFile); err != nil {
		return false
	}
	if e.onImageAttached != nil {
		fyne.Do(e.onImageAttached)
	}
	return true
}

// pasteImageDarwin reads PNG from macOS clipboard via osascript.
func pasteImageDarwin(tmpFile string) bool {
	script := `try
	set pngData to the clipboard as «class PNGf»
	set theFile to open for access POSIX file "` + tmpFile + `" with write permission
	write pngData to theFile
	close access theFile
	return true
on error
	return false
end try`
	out, err := exec.Command("osascript", "-e", script).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// pasteImageLinux reads PNG from Linux clipboard via xclip (X11) or wl-paste (Wayland).
func pasteImageLinux(tmpFile string) bool {
	out, err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output()
	if err == nil && len(out) > 0 {
		return os.WriteFile(tmpFile, out, 0644) == nil
	}
	out, err = exec.Command("wl-paste", "--type", "image/png").Output()
	if err == nil && len(out) > 0 {
		return os.WriteFile(tmpFile, out, 0644) == nil
	}
	return false
}

// pasteImageWindows reads PNG from Windows clipboard via PowerShell.
func pasteImageWindows(tmpFile string) bool {
	psScript := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -ne $null) {
	$img.Save('` + tmpFile + `', [System.Drawing.Imaging.ImageFormat]::Png)
	'true'
} else {
	'false'
}`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", psScript).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// ── ChatView ─────────────────────────────────────────

type ChatView struct {
	bridge     *AgentBridge
	ui         *UIState
	app        *App
	stopCh     chan struct{} // signals statusLoop to stop
	lastStatus string        // dedup status bar updates

	entry        *sendEntry
	sendBtn      *widget.Button
	cancelBtn    *widget.Button
	imageBtn     *widget.Button
	imagePreview *canvas.Image
	imageBar     *fyne.Container

	scroll *container.Scroll
	vbox   *fyne.Container

	tabs             *container.AppTabs
	tabMap           map[string]*container.TabItem
	agentScrolls     map[string]*container.Scroll
	agentPanelHashes map[string]string

	// Precise update tracking
	msgWidgets  []fyne.CanvasObject
	toolWidgets map[string]*toolWidgetRef
	streamW     *markdownx.MarkdownWidget
	thinkingW   fyne.CanvasObject // pulsing "thinking..." indicator
	reasoningW  *widget.Accordion // collapsible reasoning panel
	reasoningMD *markdownx.MarkdownWidget

	// Per-agent incremental state
	agentStates map[string]*agentPanelState
}

// toolWidgetRef holds mutable refs for updating a tool call in place.
type toolWidgetRef struct {
	icon      *widget.Icon
	body      *fyne.Container
	acc       *widget.Accordion
	toolName  string
	toolID    string
	rawArgs   string
	hasResult bool
}

// agentPanelState tracks incremental rendering per agent tab.
type agentPanelState struct {
	renderedEvents int                    // number of events already rendered
	toolWidgets    map[int]*toolWidgetRef // tool_call event index → ref
	vbox           *fyne.Container
	scroll         *container.Scroll
	textMD         *markdownx.MarkdownWidget
	reasoningMD    *markdownx.MarkdownWidget
}

func NewChatView(app *App, bridge *AgentBridge, ui *UIState) *ChatView {
	cv := &ChatView{
		bridge:           bridge,
		ui:               ui,
		tabMap:           make(map[string]*container.TabItem),
		agentScrolls:     make(map[string]*container.Scroll),
		agentPanelHashes: make(map[string]string),
		toolWidgets:      make(map[string]*toolWidgetRef),
		agentStates:      make(map[string]*agentPanelState),
	}

	cv.entry = newSendEntry()
	cv.entry.Wrapping = fyne.TextWrapWord
	cv.entry.SetMinRowsVisible(2)
	cv.entry.onSend = cv.onSend
	cv.entry.onImageAttached = func() {
		cv.imageBtn.Importance = widget.HighImportance
		cv.imageBtn.Refresh()
		cv.imageBar.Show()
	}

	cv.sendBtn = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), cv.onSend)
	cv.sendBtn.Importance = widget.HighImportance

	cv.cancelBtn = widget.NewButtonWithIcon("Stop", theme.CancelIcon(), func() {
		cv.bridge.Cancel()
	})
	cv.cancelBtn.Importance = widget.DangerImportance
	cv.cancelBtn.Hide()

	cv.imageBtn = widget.NewButtonWithIcon("", theme.FileImageIcon(), func() {
		w := fyne.CurrentApp().Driver().AllWindows()[0]
		d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			defer rc.Close()
			if e := cv.entry.attachImage(rc.URI().Path()); e != nil {
				return
			}
			cv.imageBtn.Importance = widget.HighImportance
			cv.imageBtn.Refresh()
			cv.imageBar.Show()
		}, w)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"}))
		d.Resize(fyne.NewSize(900, 600))
		d.Show()
	})

	return cv
}

func (cv *ChatView) Render() fyne.CanvasObject {
	btnRow := container.NewHBox(cv.cancelBtn, cv.imageBtn, cv.sendBtn)
	inputBar := container.NewBorder(nil, nil, nil, btnRow, cv.entry)

	// Image preview bar above input (hidden until image attached).
	removeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		cv.entry.clearImage()
		cv.imageBar.Hide()
		cv.imageBtn.Importance = widget.MediumImportance
		cv.imageBtn.Refresh()
	})
	cv.imageBar = container.NewHBox(
		widget.NewIcon(theme.ContentAddIcon()),
		widget.NewLabel("Image attached"),
		removeBtn,
	)
	cv.imageBar.Hide()

	inputSection := container.NewVBox(cv.imageBar, inputBar)

	cv.vbox = container.NewVBox()
	cv.scroll = container.NewVScroll(cv.vbox)

	mainTab := container.NewTabItem("Main", cv.scroll)
	cv.tabs = container.NewAppTabs(mainTab)
	cv.tabs.SetTabLocation(container.TabLocationTop)

	// Event-driven: UI mutations call handleEvent directly via fyne.Do.
	cv.ui.OnEvent = func(e UIEvent) {
		fyne.Do(func() { cv.handleEvent(e) })
	}

	// Lightweight status bar updater.
	go cv.statusLoop()

	return container.NewBorder(nil, container.NewPadded(inputSection), nil, nil, cv.tabs)
}

// ── Event handler ─────────────────────────────────────

func extractMarkdownWidget(obj fyne.CanvasObject) (*markdownx.MarkdownWidget, bool) {
	switch v := obj.(type) {
	case *markdownx.MarkdownWidget:
		return v, true
	case *fyne.Container:
		for _, child := range v.Objects {
			if md, ok := extractMarkdownWidget(child); ok {
				return md, true
			}
		}
	}
	return nil, false
}

func (cv *ChatView) onSend() {
	text := cv.entry.pendingText
	cv.entry.pendingText = ""
	if text == "" {
		text = strings.TrimSpace(cv.entry.Text)
	}
	if text == "" && cv.entry.pendingImage == nil {
		return
	}
	cv.entry.SetText("")
	img := cv.entry.pendingImage
	cv.entry.clearImage()
	cv.imageBtn.Importance = widget.MediumImportance
	cv.imageBtn.Refresh()
	cv.imageBar.Hide()
	cv.entry.Refresh()

	var content []provider.ContentBlock
	displayText := text
	if img != nil {
		pathHint := "\n\n[Attached image]\nAn image is attached directly to this message. Prefer native vision understanding first."
		if text == "" {
			text = pathHint
			displayText = "[image]"
		} else {
			text = text + pathHint
			displayText = "[image] " + displayText
		}
	}
	if text != "" {
		content = append(content, provider.TextBlock(text))
	}
	if img != nil {
		content = append(content, *img)
	}
	cv.ui.AppendChat(ChatMessage{Role: "user", Content: displayText, Time: time.Now()})

	// Echo user message to IM channels.
	if cv.bridge.Emitter != nil {
		cv.bridge.Emitter.EmitUserText(displayText)
	}

	if cv.bridge.IsWorking() {
		cv.bridge.QueueMessage(text)
		cv.ui.AppendChat(ChatMessage{Role: "system", Content: "(queued)", Time: time.Now()})
		return
	}
	// Show thinking indicator while waiting for agent response.
	cv.showThinking()

	// Push user message to mobile (only from desktop, not echo)
	cv.bridge.PushUserMessageToMobile(text)

	if err := cv.bridge.SendContent(content); err != nil {
		cv.ui.AppendChat(ChatMessage{Role: "error", Content: err.Error(), Time: time.Now()})
	}
}

func (cv *ChatView) handleEvent(e UIEvent) {
	switch e.Type {
	case EventAppend:
		cv.onAppend(e.Msg)
	case EventAssistantChunk:
		cv.onAssistantChunk(e.Text)
	case EventToolResultUpdate:
		cv.onToolResult(e.ToolID, e.Result, e.IsError)
	case EventStreamDone:
		cv.onStreamDone()
	case EventAgentUpdate:
		cv.rebuildAgentTabs()
	case EventReasoning:
		cv.onReasoningChunk(e.Text)
	}
	cv.updateButtons(cv.bridge.IsWorking())
	if cv.bridge.IsWorking() {
		cv.scroll.ScrollToBottom()
	}
}

func (cv *ChatView) onAppend(msg ChatMessage) {
	// Hide thinking when agent starts responding (not on user/system messages).
	if msg.Role == "assistant" || msg.Role == "tool" {
		cv.hideThinking()
		cv.collapseReasoning()
	}
	w := cv.renderMessage(&msg)
	if w == nil {
		return
	}
	cv.msgWidgets = append(cv.msgWidgets, w)

	// Register tool ref.
	if msg.Role == "tool" && msg.ToolID != "" {
		ref := cv.buildToolRef(&msg, w)
		if ref != nil {
			cv.toolWidgets[msg.ToolID] = ref
		}
	}

	// Track streaming assistant widget.
	if msg.Role == "assistant" && msg.Streaming {
		if md, ok := extractMarkdownWidget(w); ok {
			cv.streamW = md
		}
	}

	cv.vbox.Add(w)
	cv.scroll.ScrollToBottom()
}

func (cv *ChatView) onAssistantChunk(text string) {
	cv.hideThinking()
	cv.collapseReasoning()
	if cv.streamW != nil {
		cv.streamW.AppendChunk(text)
		cv.scroll.ScrollToBottom()
		return
	}
	// First chunk — onAppend already created the widget.
	if len(cv.msgWidgets) > 0 {
		if md, ok := extractMarkdownWidget(cv.msgWidgets[len(cv.msgWidgets)-1]); ok {
			cv.streamW = md
			md.AppendChunk(text)
			cv.scroll.ScrollToBottom()
		}
	}
}

// showThinking displays a "Thinking..." indicator with animated dots.
func (cv *ChatView) showThinking() {
	cv.hideThinking()
	icon := widget.NewIcon(theme.ComputerIcon())
	label := widget.NewLabel("Thinking...")
	label.TextStyle = fyne.TextStyle{Italic: true}
	row := container.NewHBox(icon, label)
	cv.thinkingW = row
	cv.vbox.Add(row)
	cv.vbox.Refresh()
	cv.scroll.ScrollToBottom()

	// Animate dots.
	dots := []string{".", "..", "..."}
	go func() {
		i := 0
		for cv.thinkingW != nil {
			time.Sleep(500 * time.Millisecond)
			if cv.thinkingW == nil {
				return
			}
			i = (i + 1) % 3
			text := "Thinking" + dots[i]
			fyne.Do(func() {
				if cv.thinkingW != nil {
					row.Objects[1].(*widget.Label).SetText(text)
				}
			})
		}
	}()
}

// hideThinking removes the "Thinking..." indicator.
func (cv *ChatView) hideThinking() {
	if cv.thinkingW == nil {
		return
	}
	cv.vbox.Remove(cv.thinkingW)
	cv.thinkingW = nil
	cv.vbox.Refresh()
}

// onReasoningChunk accumulates reasoning text into a collapsible panel.
func (cv *ChatView) onReasoningChunk(text string) {
	// Filter out Anthropic redacted thinking blocks.
	if text == "__redacted_thinking__" {
		return
	}
	cv.hideThinking()

	if cv.reasoningW == nil {
		// First chunk: create accordion with streaming markdown.
		md := newMD(text)
		accordion := widget.NewAccordion(widget.NewAccordionItem("Thinking...", md))
		accordion.Open(0)
		cv.reasoningW = accordion
		cv.reasoningMD = md
		cv.vbox.Add(accordion)
		cv.vbox.Refresh()
		cv.scroll.ScrollToBottom()
	} else {
		if cv.reasoningMD != nil {
			cv.reasoningMD.AppendChunk(text)
		}
		cv.scroll.ScrollToBottom()
	}
}

// collapseReasoning collapses the reasoning accordion and updates its title.
func (cv *ChatView) collapseReasoning() {
	if cv.reasoningW == nil {
		return
	}
	items := cv.reasoningW.Items
	if len(items) > 0 {
		items[0].Title = "Thought"
	}
	cv.reasoningW.CloseAll()
	cv.reasoningW.Refresh()
	cv.reasoningW = nil
	cv.reasoningMD = nil
}

func (cv *ChatView) onToolResult(toolID, result string, isError bool) {
	ref, ok := cv.toolWidgets[toolID]
	if !ok {
		return
	}
	ref.hasResult = true

	// Update icon.
	if isError {
		ref.icon.SetResource(theme.CancelIcon())
	} else {
		ref.icon.SetResource(theme.ConfirmIcon())
	}
	ref.icon.Refresh()

	// Add result accordion if applicable.
	cv.addToolResult(ref, result)
	cv.scroll.ScrollToBottom()
}

func (cv *ChatView) onStreamDone() {
	cv.streamW = nil
	// Cancel any tools still pending.
	for _, ref := range cv.toolWidgets {
		if !ref.hasResult {
			ref.icon.SetResource(theme.CancelIcon())
			ref.icon.Refresh()
		}
	}
}

// statusLoop updates status bar periodically (lightweight).
func (cv *ChatView) statusLoop() {
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-cv.stopCh:
			return
		case <-ticker.C:
			cv.ui.FlushStream()
			cv.ui.FlushReasoning()
			working := cv.bridge.IsWorking()
			fyne.Do(func() {
				cv.updateStatusBar(working)
				cv.updateButtons(working)
				// Update sidebar stats and provider enabled state.
				if cv.app != nil && cv.app.sidebarRef != nil {
					cv.app.sidebarRef.RefreshStats()
				}
				if working {
					cv.scroll.ScrollToBottom()
				}
			})
		}
	}
}

const placeholderIdle = "Message ggcode... (Enter to send, Shift+Enter for newline)"
const placeholderBusy = "ggcode is working... (messages will be queued)"

func (cv *ChatView) updateButtons(working bool) {
	cv.entry.busy = working
	if working {
		cv.cancelBtn.Show()
		cv.entry.PlaceHolder = placeholderBusy
	} else {
		cv.cancelBtn.Hide()
		cv.entry.PlaceHolder = placeholderIdle
	}
	cv.sendBtn.Show()
}

func (cv *ChatView) updateStatusBar(working bool) {
	resolved := cv.bridge.Resolved()
	tc := cv.bridge.TokenCount()
	cw := cv.bridge.ContextWindow()
	var text string
	if working {
		text = fmt.Sprintf("%s/%s | %s/%s | working (%s)",
			resolved.VendorID, resolved.Model,
			humanizeTokens(tc), humanizeTokens(cw),
			cv.bridge.Elapsed().Round(time.Second))
	} else {
		text = fmt.Sprintf("%s/%s | %s/%s",
			resolved.VendorID, resolved.Model,
			humanizeTokens(tc), humanizeTokens(cw))
	}
	if text != cv.lastStatus {
		cv.lastStatus = text
		cv.ui.SetStatusDirect(text)
	}
}

// ── Tool result live update ────────────────────────────

func (cv *ChatView) buildToolRef(msg *ChatMessage, w fyne.CanvasObject) *toolWidgetRef {
	ref := &toolWidgetRef{
		toolName: msg.ToolName,
		toolID:   msg.ToolID,
		rawArgs:  raw(msg),
	}
	// Walk widget tree to find icon and the content VBox inside iconRow's Border.
	findToolRefs(w, ref)
	return ref
}

// findToolRefs walks the widget tree depth-first.
// The structure is: Border(icon, VBox(header, [acc]))
// We need: the Icon, and the VBox (body) to add children later.
func findToolRefs(obj fyne.CanvasObject, ref *toolWidgetRef) {
	switch v := obj.(type) {
	case *widget.Icon:
		ref.icon = v
	case *widget.Accordion:
		ref.acc = v
	case *fyne.Container:
		// Check if this is a VBox that contains a toolHeader (RichText).
		// That VBox is our body for adding accordion items.
		if ref.body == nil {
			for _, child := range v.Objects {
				if _, ok := child.(*widget.RichText); ok {
					ref.body = v
					break
				}
			}
		}
		for _, child := range v.Objects {
			findToolRefs(child, ref)
		}
	}
}

func (cv *ChatView) addToolResult(ref *toolWidgetRef, result string) {
	if result == "" {
		return
	}
	tc := classifyToolGUI(ref.toolName)
	// Truly header-only tools: no result display needed.
	if tc == tcSuppress || tc == tcTodo {
		return
	}
	// Web, start/stop command, list_agents: suppress output entirely.
	if tc == tcWeb || ref.toolName == "start_command" || ref.toolName == "stop_command" || ref.toolName == "list_commands" || ref.toolName == "list_agents" {
		return
	}

	// Format result based on tool type.
	formatted := cv.formatToolResult(ref.toolName, result)
	if formatted == "" {
		return
	}

	resultBlock := newMD("```\n" + truncateRunes(formatted, 3000, "\n...(truncated)") + "\n```")
	label := "Output"
	if tc == tcFile {
		label = "Content"
	}

	if ref.acc != nil {
		ref.acc.Append(wrapAccordionItem(label, resultBlock))
	} else if ref.body != nil {
		// No accordion yet — create one with the result.
		ref.acc = widget.NewAccordion(wrapAccordionItem(label, resultBlock))
		ref.body.Add(ref.acc)
	}
}

// ── Message rendering ────────────────────────────────

func (cv *ChatView) renderMessage(msg *ChatMessage) fyne.CanvasObject {
	switch msg.Role {
	case "user":
		return cv.renderUser(msg)
	case "assistant":
		return cv.renderAssistant(msg)
	case "tool":
		return cv.renderTool(msg)
	case "system":
		return cv.renderSystem(msg)
	case "reasoning":
		return cv.renderReasoning(msg)
	case "error":
		return cv.renderError(msg)
	}
	return nil
}

func (cv *ChatView) renderUser(msg *ChatMessage) fyne.CanvasObject {
	rt := widget.NewRichTextFromMarkdown(msg.Content)
	rt.Wrapping = fyne.TextWrapWord
	return cv.iconRow(theme.AccountIcon(), rt)
}

func (cv *ChatView) renderAssistant(msg *ChatMessage) fyne.CanvasObject {
	text := msg.Content
	if text == "" && msg.Streaming {
		text = "..."
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// Linkify bare file paths in the message text
	text = linkifyFilePaths(text, cv.app)
	md := newMD(text)
	// Intercept file:// hyperlink clicks
	if cv.app != nil {
		interceptFileLinks(md, cv.app)
	}
	return cv.iconRow(theme.ComputerIcon(), md)
}

func (cv *ChatView) renderSystem(msg *ChatMessage) fyne.CanvasObject {
	t := canvas.NewText(msg.Content, theme.DisabledColor())
	t.TextStyle = fyne.TextStyle{Italic: true}
	t.TextSize = theme.Size(theme.SizeNameCaptionText)
	t.Alignment = fyne.TextAlignCenter
	return container.NewPadded(t)
}

func (cv *ChatView) renderReasoning(msg *ChatMessage) fyne.CanvasObject {
	t := canvas.NewText("Thinking: "+msg.Content, theme.DisabledColor())
	t.TextStyle = fyne.TextStyle{Italic: true}
	t.TextSize = theme.Size(theme.SizeNameCaptionText)
	return container.NewPadded(t)
}

func (cv *ChatView) renderError(msg *ChatMessage) fyne.CanvasObject {
	t := canvas.NewText("Error: "+msg.Content, theme.ErrorColor())
	t.TextSize = theme.TextSize()
	return cv.iconRow(theme.CancelIcon(), t)
}

// ── Tool rendering (mirrors TUI classifyTool logic) ──

// toolClass mirrors TUI's tool classification.
type toolClass int

const (
	tcBash     toolClass = iota // run_command
	tcFile                      // read/write/edit/notebook_edit
	tcSearch                    // grep/glob/search_files
	tcList                      // list_directory
	tcWeb                       // web_fetch/web_search
	tcGit                       // git_*
	tcCmd                       // start_command, read_command_output, wait_command, etc.
	tcLSP                       // lsp_*
	tcTodo                      // todo_write (special)
	tcAgent                     // spawn_agent
	tcMessage                   // send_message
	tcWait                      // wait_agent
	tcTeammate                  // teammate_spawn/shutdown/list/results
	tcSwarm                     // swarm_task_create/claim/complete/list
	tcSuppress                  // header-only tools (save_memory, config, skill, etc.)
	tcGeneric                   // fallback
)

func classifyToolGUI(name string) toolClass {
	switch name {
	case "run_command", "bash", "Bash":
		return tcBash
	case "read_file", "view", "write_file", "edit_file", "multi_edit_file", "notebook_edit":
		return tcFile
	case "search_files", "grep", "glob", "find":
		return tcSearch
	case "list_directory":
		return tcList
	case "web_fetch", "web_search":
		return tcWeb
	case "git_status", "git_diff", "git_log", "git_show", "git_blame",
		"git_branch_list", "git_remote", "git_stash_list", "git_add",
		"git_commit", "git_stash":
		return tcGit
	case "start_command", "read_command_output", "wait_command",
		"stop_command", "write_command_input", "list_commands":
		return tcCmd
	case "todo_write":
		return tcTodo
	case "spawn_agent":
		return tcAgent
	case "send_message":
		return tcMessage
	case "wait_agent", "list_agents":
		return tcWait
	case "teammate_spawn", "teammate_shutdown", "teammate_list", "teammate_results":
		return tcTeammate
	case "swarm_task_create", "swarm_task_claim", "swarm_task_complete", "swarm_task_list",
		"team_create", "team_delete":
		return tcSwarm
	case "save_memory", "config", "skill",
		"enter_plan_mode", "enter_worktree", "exit_worktree",
		"task_create", "task_get", "task_update", "task_list", "task_stop",
		"cron_create", "cron_delete", "cron_list",
		"list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource",
		"ask_user":
		return tcSuppress
	default:
		if strings.HasPrefix(name, "lsp_") {
			return tcLSP
		}
		if strings.HasPrefix(name, "mcp__") {
			return tcSuppress
		}
		return tcGeneric
	}
}
func (cv *ChatView) renderTool(msg *ChatMessage) fyne.CanvasObject {
	switch classifyToolGUI(msg.ToolName) {
	case tcBash:
		return cv.renderBashTool(msg)
	case tcFile:
		return cv.renderFileTool(msg)
	case tcSearch, tcList, tcWeb:
		return cv.renderHeaderOnlyTool(msg)
	case tcGit:
		return cv.renderGitTool(msg)
	case tcCmd:
		return cv.renderHeaderOnlyTool(msg)
	case tcLSP:
		return cv.renderHeaderOnlyTool(msg)
	case tcTodo:
		return cv.renderTodoTool(msg)
	case tcAgent:
		return cv.renderAgentTool(msg)
	case tcMessage:
		return cv.renderSendMessageTool(msg)
	case tcWait:
		return cv.renderHeaderOnlyTool(msg)
	case tcTeammate:
		return cv.renderHeaderOnlyTool(msg)
	case tcSwarm:
		return cv.renderSwarmTaskTool(msg)
	case tcSuppress:
		return cv.renderHeaderOnlyTool(msg)
	default:
		return cv.renderGenericTool(msg)
	}
}

// ── Tool renderers ───────────────────────────────────

// renderBashTool: description header + command + result in accordion (collapsed by default).
func (cv *ChatView) renderBashTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	cmd := extractJSONField(raw(msg), "command")

	// Fallback: use first comment line from command as description.
	if desc == "" && cmd != "" {
		desc = firstCommentLine(cmd)
	}
	if desc == "" {
		desc = "Bash"
	}
	header := cv.toolHeader(desc, msg)

	var accItems []*widget.AccordionItem

	if cmd != "" {
		cmdBlock := newMD("```bash\n" + cmd + "\n```")
		accItems = append(accItems, wrapAccordionItem("Command", cmdBlock))
	}

	if msg.Content != "" {
		result := truncateRunes(msg.Content, 3000, "\n...(truncated)")
		resultBlock := newMD("```\n" + result + "\n```")
		accItems = append(accItems, wrapAccordionItem("Output", resultBlock))
	}

	if len(accItems) > 0 {
		acc := widget.NewAccordion(accItems...)
		return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
	}
	return cv.iconRow(toolIcon(msg), container.NewVBox(header))
}

// newCodeBlock wraps text in a markdown code block.
func newCodeBlock(result string) fyne.CanvasObject {
	return newMD("```\n" + result + "\n```")
}

// renderHeaderOnlyTool: header + optional result accordion.
func (cv *ChatView) renderHeaderOnlyTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), container.NewVBox(header))
	}

	result := truncateRunes(msg.Content, 2000, "...")
	resultBlock := newCodeBlock(result)
	acc := widget.NewAccordion(wrapAccordionItem("Output", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// renderFileTool: header + line count / edit summary + result in accordion.
func (cv *ChatView) renderFileTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), container.NewVBox(header))
	}

	// Show file result in accordion.
	result := truncateRunes(msg.Content, 3000, "\n...(truncated)")
	resultBlock := newMD("```\n" + result + "\n```")
	acc := widget.NewAccordion(wrapAccordionItem("Content", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// renderGitTool: header + result in accordion (for git_diff, git_log, git_status).
func (cv *ChatView) renderGitTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), container.NewVBox(header))
	}

	result := truncateRunes(msg.Content, 2000, "...")
	resultBlock := newCodeBlock(result)
	acc := widget.NewAccordion(wrapAccordionItem("Output", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// renderGenericTool: header + result in accordion (no raw JSON).
func (cv *ChatView) renderGenericTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), container.NewVBox(header))
	}

	result := truncateRunes(msg.Content, 2000, "...")
	// Wrap raw JSON in code block for readability
	trimmed := strings.TrimSpace(result)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		resultBlock := newMD("```json\n" + result + "\n```")
		acc := widget.NewAccordion(wrapAccordionItem("Result", resultBlock))
		return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
	}
	resultBlock := newMD("```\n" + result + "\n```")
	acc := widget.NewAccordion(wrapAccordionItem("Result", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// renderTodoTool: checkbox list, no tool name header.
func (cv *ChatView) renderTodoTool(msg *ChatMessage) fyne.CanvasObject {
	var input struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	args := msg.ToolArgs
	if args == "" {
		args = msg.Content
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil || len(input.Todos) == 0 {
		return nil
	}
	var sb strings.Builder
	for _, t := range input.Todos {
		switch t.Status {
		case "done":
			sb.WriteString("- [x] " + t.Content)
		case "in_progress":
			sb.WriteString("- [ ] **" + t.Content + "** _(in progress)_")
		default:
			sb.WriteString("- [ ] " + t.Content)
		}
		sb.WriteString("\n")
	}
	rt := newMD(sb.String())
	return cv.iconRow(theme.CheckButtonCheckedIcon(), rt)
}

// renderAgentTool: agent name + task description.
func (cv *ChatView) renderAgentTool(msg *ChatMessage) fyne.CanvasObject {
	name := extractJSONField(raw(msg), "name")
	if name == "" {
		name = extractJSONField(raw(msg), "subagent_type")
	}
	if name == "" {
		name = "agent"
	}
	task := truncateRunes(extractJSONField(raw(msg), "task"), 100, "...")
	desc := "Agent: " + name
	if task != "" {
		desc += " — " + task
	}
	return cv.iconRow(toolIcon(msg), cv.toolHeader(desc, msg))
}

// renderSendMessageTool: to + summary + message preview.
func (cv *ChatView) renderSendMessageTool(msg *ChatMessage) fyne.CanvasObject {
	to := extractJSONField(raw(msg), "to")
	summary := extractJSONField(raw(msg), "summary")
	desc := "Send to: " + to
	if summary != "" {
		desc = summary
	}
	return cv.iconRow(toolIcon(msg), cv.toolHeader(desc, msg))
}

// renderSwarmTaskTool: subject + assignee header, result in accordion.
func (cv *ChatView) renderSwarmTaskTool(msg *ChatMessage) fyne.CanvasObject {
	desc := msg.ToolDesc
	if desc == "" {
		desc = prettifyToolName(msg.ToolName)
	}
	header := cv.toolHeader(desc, msg)

	if msg.Content == "" {
		return cv.iconRow(toolIcon(msg), container.NewVBox(header))
	}

	result := truncateRunes(msg.Content, 2000, "...")
	resultBlock := newCodeBlock(result)
	acc := widget.NewAccordion(wrapAccordionItem("Output", resultBlock))
	return cv.iconRow(toolIcon(msg), container.NewVBox(header, acc))
}

// ── Shared helpers ───────────────────────────────────

func (cv *ChatView) iconRow(icon fyne.Resource, content fyne.CanvasObject) fyne.CanvasObject {
	ic := widget.NewIcon(icon)
	ic.Resize(fyne.NewSize(16, 16))
	return container.NewBorder(nil, nil, ic, nil, content)
}

func (cv *ChatView) toolHeader(desc string, msg *ChatMessage) *widget.RichText {
	md := "**" + desc + "**"
	rt := widget.NewRichTextFromMarkdown(md)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// wrapAccordionItem is an alias — MarkdownWidget already handles width via MinSize override.
func wrapAccordionItem(label string, content fyne.CanvasObject) *widget.AccordionItem {
	return widget.NewAccordionItem(label, content)
}

func raw(msg *ChatMessage) string {
	if msg.ToolRaw != "" {
		return msg.ToolRaw
	}
	return msg.ToolArgs
}

// firstCommentLine extracts the first '# comment' line from a shell command.
func firstCommentLine(cmd string) string {
	for _, line := range strings.Split(cmd, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

func toolIcon(msg *ChatMessage) fyne.Resource {
	if msg.Content == "" {
		return theme.MediaRecordIcon()
	}
	if msg.IsError {
		return theme.CancelIcon()
	}
	return theme.ConfirmIcon()
}

func prettifyToolName(name string) string {
	m := map[string]string{
		"run_command": "Bash", "read_file": "Read", "write_file": "Write",
		"edit_file": "Edit", "multi_edit_file": "Edit", "search_files": "Grep",
		"glob": "Glob", "find": "Glob", "list_directory": "List",
		"web_search": "Search", "web_fetch": "Fetch",
		"start_command": "Bash", "stop_command": "Stop",
		"read_command_output": "Output", "wait_command": "Wait",
		"write_command_input": "Input", "list_commands": "Jobs",
		"todo_write": "To-Do", "spawn_agent": "Agent",
		"send_message": "Send", "wait_agent": "Wait",
		"list_agents": "Agents", "teammate_spawn": "Teammate",
		"teammate_shutdown": "Shutdown", "teammate_list": "Teammates",
		"teammate_results": "Results", "swarm_task_create": "Task",
		"swarm_task_claim": "Claim", "swarm_task_complete": "Complete",
		"swarm_task_list": "Tasks", "team_create": "Team",
		"save_memory": "Memory", "config": "Config", "skill": "Skill",
		"git_status": "Git Status", "git_diff": "Git Diff",
		"git_log": "Git Log", "git_show": "Git Show",
		"git_blame": "Git Blame", "git_add": "Git Add",
		"git_commit": "Git Commit", "git_stash": "Git Stash",
		"git_branch_list": "Branches", "git_remote": "Remote",
		"notebook_edit": "Notebook",
	}
	if v, ok := m[name]; ok {
		return v
	}
	if strings.HasPrefix(name, "lsp_") {
		return strings.Title(name[4:])
	}
	if len(name) > 0 {
		// Title case: replace _ with space, capitalize each word
		words := strings.Split(strings.ReplaceAll(name, "_", " "), " ")
		for i, w := range words {
			if len(w) > 0 {
				words[i] = strings.ToUpper(w[:1]) + w[1:]
			}
		}
		return strings.Join(words, " ")
	}
	return name
}

// ── Agent tabs ───────────────────────────────────────

// ── Agent panel (incremental, same rendering as main) ──

func (cv *ChatView) rebuildAgentTabs() {
	cv.ui.RemoveStalePanels()

	panels := cv.ui.GetAgentPanels()
	activeIDs := make(map[string]bool)

	for _, panel := range panels {
		activeIDs[panel.ID] = true

		st, exists := cv.agentStates[panel.ID]
		if !exists {
			// New agent — create state + tab.
			st = &agentPanelState{
				toolWidgets: make(map[int]*toolWidgetRef),
			}
			st.vbox = container.NewVBox()
			st.scroll = container.NewVScroll(st.vbox)
			cv.agentStates[panel.ID] = st
			cv.agentScrolls[panel.ID] = st.scroll

			tabName := agentTabName(panel, panels)
			item := container.NewTabItem(tabName, st.scroll)
			cv.tabMap[panel.ID] = item
			cv.tabs.Append(item)

			// Render header.
			cv.renderAgentHeader(panel, st.vbox)
		}

		// Update tab name (status indicator).
		if item, ok := cv.tabMap[panel.ID]; ok {
			item.Text = agentTabName(panel, panels)
		}

		// Incremental: render only new events since last time.
		if len(panel.Events) > st.renderedEvents {
			cv.appendAgentEvents(panel, st, st.renderedEvents)
			st.renderedEvents = len(panel.Events)
		}

		// Render final result if panel completed.
		if panel.Result != "" && panel.Status != "running" && panel.Status != "working" {
			if len(st.vbox.Objects) > 0 {
				// Only add result once — check if last object is already the result.
				last := st.vbox.Objects[len(st.vbox.Objects)-1]
				if lbl, ok := last.(*widget.Label); !ok || lbl.Text != panel.Result {
					objs := cv.renderAgentResult(panel)
					for _, o := range objs {
						st.vbox.Add(o)
					}
				}
			}
		}

		st.scroll.ScrollToBottom()
	}

	// Remove stale tabs.
	for id, item := range cv.tabMap {
		if !activeIDs[id] {
			cv.tabs.Remove(item)
			delete(cv.tabMap, id)
			delete(cv.agentScrolls, id)
			delete(cv.agentPanelHashes, id)
			delete(cv.agentStates, id)
		}
	}
	cv.tabs.Refresh()
}

func (cv *ChatView) renderAgentHeader(panel AgentPanelData, vbox *fyne.Container) {
	statusColor := theme.ColorNameSuccess
	if panel.Status == "running" || panel.Status == "working" {
		statusColor = theme.ColorNameWarning
	} else if panel.Status == "failed" {
		statusColor = theme.ColorNameError
	}
	header := widget.NewRichText(
		&widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}, Text: panel.Task},
		&widget.TextSegment{Style: widget.RichTextStyle{ColorName: statusColor}, Text: "  " + panel.Status},
	)
	header.Wrapping = fyne.TextWrapWord
	vbox.Add(cv.iconRow(theme.ComputerIcon(), header))
}

// appendAgentEvents renders only new events incrementally.
// Uses the same renderTool as main panel for consistent look.
func (cv *ChatView) appendAgentEvents(panel AgentPanelData, st *agentPanelState, fromIdx int) {
	for i := fromIdx; i < len(panel.Events); i++ {
		ev := &panel.Events[i]

		switch ev.Type {
		case "text":
			st.reasoningMD = nil
			if ev.Content != "" {
				if st.textMD == nil {
					st.textMD = newMD(ev.Content)
					st.vbox.Add(cv.iconRow(theme.ComputerIcon(), st.textMD))
				} else {
					st.textMD.AppendChunk(ev.Content)
				}
			}

		case "tool_call":
			st.textMD = nil
			st.reasoningMD = nil

			msg := &ChatMessage{
				Role:     "tool",
				ToolName: ev.ToolName,
				ToolDesc: toolDescription(ev.ToolName, ev.ToolArgs),
				ToolArgs: toolArgSummary(ev.ToolName, ev.ToolArgs),
				ToolRaw:  ev.ToolArgs,
				ToolID:   ev.ToolID,
			}
			w := cv.renderTool(msg)

			if w != nil {
				ref := cv.buildToolRef(msg, w)
				if ref != nil {
					st.toolWidgets[i] = ref
				}
				st.vbox.Add(w)
			}

		case "tool_result":
			st.textMD = nil
			st.reasoningMD = nil
			// Find the tool_call ref by matching ToolID directly on the ref.
			for _, ref := range st.toolWidgets {
				if ref.hasResult || ref.toolID != ev.ToolID {
					continue
				}
				ref.hasResult = true
				if ev.IsError {
					ref.icon.SetResource(theme.CancelIcon())
				} else {
					ref.icon.SetResource(theme.ConfirmIcon())
				}
				ref.icon.Refresh()
				cv.addToolResult(ref, ev.Content)
				break
			}

		case "error":
			st.textMD = nil
			st.reasoningMD = nil
			t := canvas.NewText(ev.Content, theme.ErrorColor())
			t.TextSize = theme.TextSize()
			st.vbox.Add(cv.iconRow(theme.CancelIcon(), t))

		case "reasoning":
			st.textMD = nil
			if ev.Content != "" {
				if st.reasoningMD == nil {
					md := newMD(ev.Content)
					item := widget.NewAccordionItem("Thought", md)
					accordion := widget.NewAccordion(item)
					accordion.CloseAll()
					st.reasoningMD = md
					st.vbox.Add(accordion)
				} else {
					st.reasoningMD.AppendChunk(ev.Content)
				}
			}
		}
	}
}

func (cv *ChatView) renderAgentResult(panel AgentPanelData) []fyne.CanvasObject {
	if panel.Result == "" {
		return nil
	}
	if panel.Status == "failed" {
		t := canvas.NewText(panel.Result, theme.ErrorColor())
		t.TextSize = theme.TextSize()
		return []fyne.CanvasObject{cv.iconRow(theme.ComputerIcon(), t)}
	}
	return []fyne.CanvasObject{cv.iconRow(theme.ComputerIcon(), newMD("```\n"+panel.Result+"\n```"))}
}

func agentTabName(panel AgentPanelData, panels []AgentPanelData) string {
	tabName := truncateTabName(panel.Name, len(panels))
	if tabName == "" {
		tabName = truncateTabName(panel.ID, len(panels))
	}
	if panel.Status == "running" || panel.Status == "working" {
		tabName += "*"
	}
	return tabName
}

func truncateTabName(name string, totalAgents int) string {
	maxLen := 25
	switch {
	case totalAgents <= 3:
		maxLen = 25
	case totalAgents <= 6:
		maxLen = 18
	case totalAgents <= 10:
		maxLen = 12
	default:
		maxLen = 8
	}
	runes := []rune(name)
	if len(runes) <= maxLen {
		return name
	}
	return string(runes[:maxLen-1]) + "…"
}

// renderMarkdownTables converts GFM table blocks to formatted text
// since Fyne's RichText doesn't support table rendering.
func renderMarkdownTables(md string) string {
	lines := strings.Split(md, "\n")
	var result []string
	var tableLines []string
	inTable := false

	for _, line := range lines {
		if isTableLine(line) {
			if !inTable {
				inTable = true
				tableLines = []string{line}
			} else {
				tableLines = append(tableLines, line)
			}
		} else {
			if inTable {
				result = append(result, formatTable(tableLines)...)
				tableLines = nil
				inTable = false
			}
			result = append(result, line)
		}
	}
	if inTable {
		result = append(result, formatTable(tableLines)...)
	}
	return strings.Join(result, "\n")
}

func isTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Table rows start and end with |
	if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
		return true
	}
	// Separator line like |---|---|
	if strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "---") {
		return true
	}
	return false
}

func formatTable(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}

	// Parse all rows (skip separator rows).
	var rows [][]string
	for _, line := range lines {
		if isSeparatorRow(line) {
			continue
		}
		cells := parseCells(line)
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	if len(rows) == 0 {
		return lines
	}

	// Calculate column widths (use rune width for CJK).
	numCols := 0
	for _, r := range rows {
		if len(r) > numCols {
			numCols = len(r)
		}
	}
	widths := make([]int, numCols)
	for _, r := range rows {
		for i, c := range r {
			w := runeWidth(c)
			if w > widths[i] {
				widths[i] = w
			}
		}
	}

	// Format rows as code block.
	var sb strings.Builder
	for ri, r := range rows {
		for i := 0; i < numCols; i++ {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			w := runeWidth(cell)
			pad := widths[i] - w
			if pad < 0 {
				pad = 0
			}
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(cell + strings.Repeat(" ", pad))
		}
		sb.WriteString("\n")

		// Add separator after header row.
		if ri == 0 {
			for i := 0; i < numCols; i++ {
				if i > 0 {
					sb.WriteString("  ")
				}
				sb.WriteString(strings.Repeat("─", widths[i]))
			}
			sb.WriteString("\n")
		}
	}

	return []string{"```\n" + sb.String() + "```"}
}

// runeWidth returns display width of string (CJK chars = 2).
func runeWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115F || r <= 0x11A2 ||
			(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
			(r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE30 && r <= 0xFE6F) ||
			(r >= 0xFF01 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6) ||
			(r >= 0x20000 && r <= 0x2FFFD) ||
			(r >= 0x30000 && r <= 0x3FFFD)) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isSeparatorRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	for _, ch := range trimmed {
		if ch != '-' && ch != ' ' && ch != ':' {
			return false
		}
	}
	return true
}

func parseCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		result = append(result, strings.TrimSpace(p))
	}
	return result
}

// rebuildFromMessages clears the chat and rebuilds it from provider.Message history.
// Used when resuming a session.
func (cv *ChatView) rebuildFromMessages(messages []provider.Message) {
	// Clear existing state.
	cv.msgWidgets = nil
	cv.toolWidgets = make(map[string]*toolWidgetRef)
	cv.streamW = nil
	cv.vbox.Objects = nil
	cv.vbox.Refresh()

	// Collect user messages for input history.
	var userMsgs []string

	// Track tool_use blocks to match with tool_result.
	type toolUseInfo struct {
		toolName string
		rawArgs  string
		toolID   string
	}
	toolUses := make(map[string]toolUseInfo) // toolID → info

	// First pass: collect all tool_use and tool_result pairs.
	toolResults := make(map[string]string) // toolID → result
	toolErrors := make(map[string]bool)    // toolID → isError
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" && block.ToolID != "" {
				toolUses[block.ToolID] = toolUseInfo{
					toolName: block.ToolName,
					rawArgs:  string(block.Input),
					toolID:   block.ToolID,
				}
			}
			if block.Type == "tool_result" && block.ToolID != "" {
				toolResults[block.ToolID] = block.Output
				toolErrors[block.ToolID] = block.IsError
			}
		}
	}

	// Second pass: render.
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			var textParts []string
			for _, block := range msg.Content {
				if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
					textParts = append(textParts, strings.TrimSpace(block.Text))
				}
			}
			if len(textParts) > 0 {
				joined := strings.Join(textParts, "\n")
				userMsgs = append(userMsgs, joined)
				w := cv.iconRow(theme.ComputerIcon(), newMD(joined))
				cv.vbox.Add(w)
				cv.msgWidgets = append(cv.msgWidgets, w)
			}

		case "assistant":
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						w := newMD(strings.TrimSpace(block.Text))
						cv.vbox.Add(w)
						cv.msgWidgets = append(cv.msgWidgets, w)
					}
				case "tool_use":
					result := toolResults[block.ToolID]
					isErr := toolErrors[block.ToolID]
					chatMsg := &ChatMessage{
						Role:     "tool",
						ToolName: block.ToolName,
						ToolDesc: toolDescription(block.ToolName, string(block.Input)),
						ToolArgs: toolArgSummary(block.ToolName, string(block.Input)),
						ToolRaw:  string(block.Input),
						ToolID:   block.ToolID,
						Content:  result,
						IsError:  isErr,
					}
					w := cv.renderTool(chatMsg)
					if w != nil {
						ref := cv.buildToolRef(chatMsg, w)
						if ref != nil {
							cv.toolWidgets[block.ToolID] = ref
						}
						cv.vbox.Add(w)
						cv.msgWidgets = append(cv.msgWidgets, w)
					}
				}
			}
		}
	}

	cv.vbox.Refresh()

	// Defer scroll to bottom so layout is computed first.
	go func() {
		time.Sleep(200 * time.Millisecond)
		fyne.Do(func() {
			cv.scroll.Refresh()
			cv.scroll.ScrollToBottom()
		})
	}()
	cv.entry.loadHistory(userMsgs)
}

// linkifyFilePaths converts bare file paths in agent messages to clickable markdown links.
// Paths under the workspace root are converted to relative paths for display.
func linkifyFilePaths(text string, app *App) string {
	if app == nil || app.dc == nil || app.dc.WorkDir == "" {
		return text
	}
	ws := app.dc.WorkDir
	// Match absolute paths under workspace: /workspace/path/to/file.go
	wsPattern := regexp.QuoteMeta(ws)
	pat := regexp.MustCompile(`(?m)(?:^|[\s(\[` + "`" + `'"` + `])(` + wsPattern + `/[^\s)\]` + "`" + `'"` + `]+)`)
	return pat.ReplaceAllStringFunc(text, func(match string) string {
		sub := pat.FindStringSubmatch(match)
		if len(sub) < 2 || sub[1] == "" {
			return match
		}
		absPath := sub[1]
		if _, err := os.Stat(absPath); err != nil {
			return match
		}
		rel, err := filepath.Rel(ws, absPath)
		if err != nil {
			rel = absPath
		}
		// Replace just the path portion with a markdown link
		link := "[" + rel + "](file://" + absPath + ")"
		return strings.Replace(match, absPath, link, 1)
	})
}

// interceptFileLinks walks the widget tree and replaces file:// hyperlink OnTapped handlers.
func interceptFileLinks(obj fyne.CanvasObject, app *App) {
	switch w := obj.(type) {
	case *widget.Hyperlink:
		if w.URL != nil && w.URL.Scheme == "file" {
			path := w.URL.Path
			w.OnTapped = func() {
				app.showFilePreview(path, 0)
			}
		}
	case *fyne.Container:
		for _, child := range w.Objects {
			interceptFileLinks(child, app)
		}
	}
}

// formatToolResult transforms raw tool result into a human-readable summary.
func (cv *ChatView) formatToolResult(toolName, result string) string {
	switch toolName {
	case "multi_file_edit", "multi_edit_file":
		return formatMultiEditResult(result)
	case "read_command_output":
		return extractRecentOutput(result)
	default:
		return result
	}
}

// formatMultiEditResult formats multi_file_edit JSON into a human-readable summary.
func formatMultiEditResult(result string) string {
	var raw struct {
		Summary string `json:"summary"`
		Results []struct {
			Path             string `json:"path"`
			Status           string `json:"status"`
			AppliedEditCount int    `json:"applied_edit_count"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result), &raw); err != nil {
		return result
	}

	var lines []string
	lines = append(lines, raw.Summary)
	for _, r := range raw.Results {
		path := r.Path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			path = path[idx+1:]
		}
		if r.Status == "success" {
			if r.AppliedEditCount > 0 {
				lines = append(lines, fmt.Sprintf("  - %s (%d edit%s)", path, r.AppliedEditCount, pluralS(r.AppliedEditCount)))
			} else {
				lines = append(lines, fmt.Sprintf("  - %s", path))
			}
		} else {
			lines = append(lines, fmt.Sprintf("  - %s [%s]", path, r.Status))
		}
	}
	return strings.Join(lines, "\n")
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// extractRecentOutput parses the structured read_command_output result
// and returns only the "Recent output" content.
func extractRecentOutput(result string) string {
	marker := "Recent output:\n"
	idx := strings.Index(result, marker)
	if idx < 0 {
		return ""
	}
	output := result[idx+len(marker):]
	return strings.TrimSpace(output)
}
