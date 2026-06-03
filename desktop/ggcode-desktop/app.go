package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/tool"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tunnel"
)

var onboardPanelMinSize = fyne.NewSize(720, 420)

// App is the top-level desktop application state.
type App struct {
	fyneApp        fyne.App
	window         fyne.Window
	dc             *DesktopConfig
	cfg            *config.Config
	windowTitle    string
	nativeTitlebar nativeTitlebarConfig

	// IM runtime.
	imManager     *im.Manager
	imController  *im.AdapterController
	imWindow      fyne.Window
	imPairingWin  fyne.Window
	metricsWindow fyne.Window

	// Shared UI state for cross-goroutine updates.
	ui *UIState

	// UI components.
	content        *fyne.Container
	titleBarLabel  *widget.Label
	titleBarSizer  *canvas.Rectangle
	split          *container.Split
	chatViewObj    fyne.CanvasObject
	chatViewRef    *ChatView
	sidebarObj     fyne.CanvasObject
	sidebarRef     *Sidebar
	sidebarHidden  bool
	sessionLoading bool         // true while resuming a session (prevents double-click)
	filePreview    *FilePreview // currently shown file preview, or nil

	// Agent state.
	agentBridge *AgentBridge

	// Mobile tunnel.
	tunnelMu      sync.RWMutex
	tunnelSession *tunnel.Session
	tunnelBroker  *tunnel.Broker
	shareDialog   dialog.Dialog
}

type nativeTitlebarConfig struct {
	Integrated   bool
	TopInset     float32
	LeadingInset float32
}

type shareInviteRefresher interface {
	RefreshInvite(context.Context) (*tunnel.SessionInfo, error)
}

func (a *App) currentTunnelSession() *tunnel.Session {
	a.tunnelMu.RLock()
	defer a.tunnelMu.RUnlock()
	return a.tunnelSession
}

func (a *App) currentTunnelBroker() *tunnel.Broker {
	a.tunnelMu.RLock()
	defer a.tunnelMu.RUnlock()
	return a.tunnelBroker
}

func (a *App) setTunnelState(sess *tunnel.Session, broker *tunnel.Broker) {
	a.tunnelMu.Lock()
	defer a.tunnelMu.Unlock()
	a.tunnelSession = sess
	a.tunnelBroker = broker
}

func (a *App) clearTunnelState() {
	a.setTunnelState(nil, nil)
}

func (a *App) forwardTunnelUserMessage(broker *tunnel.Broker, data tunnel.MessageData) {
	text := strings.TrimSpace(data.Text)
	if text == "" {
		return
	}
	if broker != nil {
		broker.PushUserMessageData(tunnel.MessageData{
			Text:      text,
			MessageID: tunnel.NormalizeClientMessageID(data.MessageID),
		})
	}
	if a.agentBridge != nil {
		_ = a.agentBridge.Send(text)
	}
}

// NewApp creates the desktop app.
func NewApp(fyneApp fyne.App) *App {
	return &App{
		fyneApp: fyneApp,
		dc:      LoadDesktopConfig(),
		ui:      NewUIState(),
	}
}

// Run shows the window and starts the event loop.
func (a *App) Run() {
	// Initialize i18n
	loadTranslations()
	setLanguage(a.dc.Language)

	a.window = a.fyneApp.NewWindow("ggcode")
	setWindowIcon(a.window)
	a.nativeTitlebar = setupNativeTitlebar(a.window)
	a.buildUI()
	a.setupMenu()
	a.setTitle("ggcode")

	w := float32(1200)
	h := float32(800)
	if a.dc.WindowW > 0 {
		w = float32(a.dc.WindowW)
	}
	if a.dc.WindowH > 0 {
		h = float32(a.dc.WindowH)
	}
	a.window.Resize(fyne.NewSize(w, h))

	if a.dc.WorkDir == "" {
		a.showWelcome()
	} else {
		a.initFromWorkDir(a.dc.WorkDir)
	}

	a.window.SetOnClosed(func() {
		size := a.window.Canvas().Size()
		a.dc.WindowW = int(size.Width)
		a.dc.WindowH = int(size.Height)
		_ = a.dc.Save()
		if a.agentBridge != nil {
			a.agentBridge.Close()
		}
		a.closeTunnelGracefully(2 * time.Second)
	})

	a.window.Show()
	a.refreshNativeTitlebar()
	go func() {
		time.Sleep(250 * time.Millisecond)
		fyne.Do(func() {
			a.refreshNativeTitlebar()
		})
	}()
	a.fyneApp.Run()
}

// ── UI construction ──────────────────────────────────

func (a *App) buildUI() {
	// ── Compact status bar ──
	microStyle := fyne.TextStyle{Monospace: true}
	microSize := float32(10)
	dimColor := color.RGBA{R: 107, G: 114, B: 128, A: 255}
	sepColor := color.RGBA{R: 58, G: 63, B: 78, A: 255}
	accentColor := color.RGBA{R: 126, G: 184, B: 218, A: 255}
	brightColor := color.RGBA{R: 201, G: 209, B: 217, A: 255}
	greenColor := color.RGBA{R: 74, G: 222, B: 128, A: 255}

	// Helper: small colored text bound to a String binding.
	boundText := func(b binding.String, clr color.Color, size float32) *canvas.Text {
		t := canvas.NewText("", clr)
		t.TextSize = size
		t.TextStyle = microStyle
		b.AddListener(binding.NewDataListener(func() {
			v, _ := b.Get()
			fyne.Do(func() { t.Text = v; t.Refresh() })
		}))
		return t
	}
	staticText := func(s string, clr color.Color, size float32) *canvas.Text {
		t := canvas.NewText(s, clr)
		t.TextSize = size
		t.TextStyle = microStyle
		return t
	}

	// Vendor tag badge.
	vendorText := boundText(a.ui.StatusBarVendor, accentColor, microSize)
	vendorBg := newThemeBlendStrokeRect(theme.ColorNameInputBackground, theme.ColorNameOverlayBackground, 0.08, theme.ColorNameSeparator, theme.ColorNamePrimary, 0.12)
	vendorBg.CornerRadius = 4
	vendorBg.StrokeWidth = 0
	vendorBadge := container.NewStack(vendorBg, container.New(layout.NewCustomPaddedLayout(1, 1, 4, 4), vendorText))

	// Context usage.
	ctxLabel := staticText("ctx", dimColor, 9)
	ctxValue := boundText(a.ui.StatusBarContext, brightColor, microSize)

	// Token metrics.
	inputLabel := staticText("in", accentColor, 8)
	inputValue := boundText(a.ui.StatusBarInput, accentColor, microSize)
	outputLabel := staticText("out", brightColor, 8)
	outputValue := boundText(a.ui.StatusBarOutput, brightColor, microSize)
	cacheLabel := staticText("cache", greenColor, 8)
	cacheValue := boundText(a.ui.StatusBarCacheHit, greenColor, microSize)

	// Status text (right-aligned).
	statusText := boundText(a.ui.StatusBarStatus, greenColor, microSize)

	// Separators.
	sep1 := staticText("|", sepColor, microSize)
	sep2 := staticText("|", sepColor, microSize)

	// Assemble bar.
	barContent := container.New(layout.NewCustomPaddedLayout(4, 4, 10, 10),
		container.NewHBox(
			vendorBadge,
			sep1,
			ctxLabel,
			ctxValue,
			sep2,
			inputLabel,
			inputValue,
			outputLabel,
			outputValue,
			cacheLabel,
			cacheValue,
			layout.NewSpacer(),
			statusText,
		),
	)

	statusChrome := newThemeBlendStrokeRect(theme.ColorNameInputBackground, theme.ColorNameOverlayBackground, 0.08, theme.ColorNameSeparator, theme.ColorNamePrimary, 0.12)
	statusChrome.CornerRadius = 8
	statusBox := container.NewStack(statusChrome, barContent)

	a.content = container.NewStack(widget.NewLabel(""))
	topChrome := a.buildTopChrome()
	root := container.NewBorder(topChrome, compactPad(4, 6, 8, 8, statusBox), nil, nil, a.content)
	a.window.SetContent(root)
}

func (a *App) buildTopChrome() fyne.CanvasObject {
	if !a.nativeTitlebar.Integrated {
		return nil
	}
	a.titleBarLabel = widget.NewLabelWithStyle("ggcode", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.titleBarLabel.Wrapping = fyne.TextWrapOff
	a.titleBarSizer = canvas.NewRectangle(color.Transparent)
	a.titleBarSizer.SetMinSize(fyne.NewSize(0, titlebarChromeHeight(a.nativeTitlebar)))

	bg := newThemeBlendRect(theme.ColorNameBackground, theme.ColorNameInputBackground, 0.06)
	separator := newThemeBlendRect(theme.ColorNameSeparator, theme.ColorNamePrimary, 0.16)
	separator.SetMinSize(fyne.NewSize(0, 1))
	row := container.NewHBox(a.titleBarLabel, layout.NewSpacer())
	content := compactPad(8, 8, a.nativeTitlebar.LeadingInset, 16, row)
	return container.NewBorder(nil, separator, nil, nil, container.NewStack(a.titleBarSizer, bg, content))
}

func titlebarChromeHeight(cfg nativeTitlebarConfig) float32 {
	height := cfg.TopInset + 8
	if height < 36 {
		height = 36
	}
	return height
}

func (a *App) applyNativeTitlebarConfig(cfg nativeTitlebarConfig) {
	a.nativeTitlebar = cfg
	if a.titleBarSizer != nil {
		a.titleBarSizer.SetMinSize(fyne.NewSize(0, titlebarChromeHeight(cfg)))
		a.titleBarSizer.Refresh()
	}
	if a.titleBarLabel != nil {
		a.titleBarLabel.Refresh()
	}
}

func (a *App) refreshNativeTitlebar() {
	if a.window == nil {
		return
	}
	a.applyNativeTitlebarConfig(setupNativeTitlebar(a.window))
	themeName := ""
	if a.dc != nil {
		themeName = a.dc.Theme
	}
	pal := paletteForTheme(themeName)
	updateNativeTitlebarAppearance(a.window, toNRGBA(pal.HeaderBackground), toNRGBA(pal.Foreground))
	updateNativeWindowTitle(a.window, a.windowTitle)
}

func (a *App) setupMenu() {
	fileMenu := fyne.NewMenu(t("menu.file"),
		fyne.NewMenuItem(t("menu.open_project"), func() { a.showFolderPicker() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(t("menu.quit"), func() { a.fyneApp.Quit() }),
	)
	viewMenu := fyne.NewMenu(t("menu.view"),
		fyne.NewMenuItem(t("menu.toggle_sidebar"), func() { a.toggleSidebar() }),
		fyne.NewMenuItem(t("menu.refresh_stats"), func() { a.refreshSidebar() }),
		fyne.NewMenuItem(t("menu.open_metrics"), func() { a.showMetricsWindow() }),
		fyne.NewMenuItemSeparator(),
		a.buildLanguageMenu(),
		a.buildThemeMenu(),
	)
	toolsMenu := fyne.NewMenu(t("menu.tools"),
		fyne.NewMenuItem(t("menu.im_settings"), func() { a.showIMWindow() }),
	)
	helpMenu := fyne.NewMenu(t("menu.help"),
		fyne.NewMenuItem(t("menu.about"), func() { a.showAbout() }),
		fyne.NewMenuItem(t("menu.check_updates"), func() { a.openUpdates() }),
	)
	a.window.SetMainMenu(fyne.NewMainMenu(fileMenu, viewMenu, toolsMenu, helpMenu))
}

// showAbout displays the About dialog with app icon, version, and links.
func (a *App) showAbout() {
	icon := canvas.NewImageFromResource(fyne.NewStaticResource("icon.png", iconBytes))
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(96, 96))

	title := widget.NewLabelWithStyle("ggcode", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	versionLabel := widget.NewLabelWithStyle(t("about.version", Version), fyne.TextAlignCenter, fyne.TextStyle{Monospace: true})
	desc := widget.NewLabel(t("app.description"))
	desc.Alignment = fyne.TextAlignCenter

	releaseLink := widget.NewHyperlink(t("about.github_releases"), mustParseURL("https://github.com/topcheer/ggcode/releases"))
	releaseLink.Alignment = fyne.TextAlignCenter
	issuesLink := widget.NewHyperlink(t("about.report_issue"), mustParseURL("https://github.com/topcheer/ggcode/issues"))
	issuesLink.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(
		container.NewCenter(icon),
		container.NewCenter(title),
		container.NewCenter(versionLabel),
		widget.NewSeparator(),
		container.NewCenter(desc),
		widget.NewSeparator(),
		container.NewCenter(releaseLink),
		container.NewCenter(issuesLink),
	)

	dialog.ShowCustom(t("about.title"), t("about.close"), content, a.window)
}

// showShareDialog starts a tunnel and shows the connection QR code + URL.
func (a *App) showShareDialog() {
	if sess := a.currentTunnelSession(); sess != nil {
		a.runShareDialogAction(func(ctx context.Context) (*tunnel.SessionInfo, error) {
			return refreshShareInvite(ctx, sess)
		}, "tunnel invite refresh failed")
		return
	}

	a.runShareDialogAction(func(ctx context.Context) (*tunnel.SessionInfo, error) {
		sess := tunnel.NewSession(tunnel.DefaultRelayURL, tunnel.WithClientMetadata("desktop", Version))
		info, err := sess.Start(ctx)
		if err != nil {
			return nil, err
		}

		broker := tunnel.NewBroker(sess)

		// Handle commands from mobile client
		broker.OnCommand(func(cmd tunnel.GatewayMessage) {
			switch cmd.Type {
			case "user_text", "message":
				var data tunnel.MessageData
				if err := json.Unmarshal(cmd.Data, &data); err != nil {
					return
				}
				text := data.Text
				if text != "" {
					fyne.Do(func() {
						if a.ui != nil {
							a.ui.AppendChat(ChatMessage{Role: "user", Content: text, Time: time.Now()})
						}
					})
					a.forwardTunnelUserMessage(broker, data)
				}
				// Acknowledge to mobile client that the message was received by desktop.
				broker.PushServerAck(data.MessageID)
			case tunnel.CmdApprovalResponse:
				if a.agentBridge == nil {
					return
				}
				var data tunnel.ApprovalResponseData
				if err := json.Unmarshal(cmd.Data, &data); err != nil {
					return
				}
				a.agentBridge.handleMobileApprovalResponse(data)
			case tunnel.CmdAskUserResponse:
				if a.agentBridge == nil {
					return
				}
				var data tunnel.AskUserResponseData
				if err := json.Unmarshal(cmd.Data, &data); err != nil {
					return
				}
				a.agentBridge.handleMobileAskUserResponse(data)
			case tunnel.CmdLanguageChange:
				var data tunnel.LanguageChangeData
				if err := json.Unmarshal(cmd.Data, &data); err != nil {
					return
				}
				a.applyLanguageChange(data.Language)
			case tunnel.CmdThemeChange:
				var data tunnel.ThemeChangeData
				if err := json.Unmarshal(cmd.Data, &data); err != nil {
					return
				}
				a.applyThemeChange(data.Theme)
			case tunnel.CmdInterrupt:
				if a.agentBridge != nil {
					a.agentBridge.Cancel()
				}
			}
		})

		a.setTunnelState(sess, broker)
		if a.agentBridge != nil {
			a.agentBridge.ensureSession()

			snapshot := a.tunnelSnapshot()
			switchedSession := false
			if current := a.agentBridge.CurrentSession(); current != nil {
				broker.SwitchSession(current.ID)
				switchedSession = true
			}
			a.agentBridge.PrepareCurrentSessionTunnelLedger()
			replayedCanonical := false
			if events := a.agentBridge.CurrentSessionTunnelEvents(); len(events) > 0 {
				broker.ReplayEvents(events, !switchedSession)
				replayedCanonical = true
			} else {
				broker.SendSnapshot(snapshot)
			}
			broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
				return a.tunnelSnapshot()
			})
			a.agentBridge.AttachTunnelBroker(broker)
			if !replayedCanonical {
				a.reseedTunnelSnapshotAfterAttach(broker, snapshot)
			}
		}

		return info, nil
	}, "tunnel failed")
}

func refreshShareInvite(ctx context.Context, sess shareInviteRefresher) (*tunnel.SessionInfo, error) {
	if sess == nil {
		return nil, fmt.Errorf("tunnel session: nil session")
	}
	return sess.RefreshInvite(ctx)
}

func (a *App) runShareDialogAction(run func(context.Context) (*tunnel.SessionInfo, error), errPrefix string) {
	statusLabel := widget.NewLabel(t("status.establishing_tunnel"))
	statusLabel.Alignment = fyne.TextAlignCenter
	progress := widget.NewProgressBarInfinite()
	connectContent := container.NewVBox(statusLabel, progress)
	connectWin := dialog.NewCustom(t("share.title"), t("common.cancel"), connectContent, a.window)
	connectWin.Show()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		info, err := run(ctx)
		fyne.Do(func() {
			connectWin.Hide()
			if err != nil {
				dialog.ShowError(fmt.Errorf("%s: %w", errPrefix, err), a.window)
				return
			}
			if info != nil {
				a.showTunnelInfo(info)
			}
		})
	}()
}

func (a *App) tunnelSnapshot() tunnel.BrokerSnapshot {
	snapshot := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{
			Workspace: a.dc.WorkDir,
			Version:   Version,
			Language:  a.cfg.Language,
			Theme:     normalizeThemeName(a.dc.Theme),
		},
	}
	if a.agentBridge == nil {
		snapshot.Status = tunnel.StatusData{Status: tunnel.StatusIdle}
		return snapshot
	}
	snapshot.Status = a.agentBridge.CurrentTunnelStatus()
	snapshot.Activity = tunnel.ActivityData{Activity: a.agentBridge.CurrentTunnelActivity()}
	snapshot.History = a.agentBridge.CurrentTunnelSnapshotHistory()
	snapshot.ExtraEvents = a.currentTunnelAgentSnapshotEvents()
	return snapshot
}

func desktopTunnelSnapshotMatches(a, b tunnel.BrokerSnapshot) bool {
	if a.SessionInfo != b.SessionInfo || a.Status != b.Status || a.Activity != b.Activity {
		return false
	}
	if !desktopTunnelHistoryMatches(a.History, b.History) {
		return false
	}
	if len(a.ExtraEvents) != len(b.ExtraEvents) {
		return false
	}
	for i := range a.ExtraEvents {
		if a.ExtraEvents[i].Type != b.ExtraEvents[i].Type ||
			a.ExtraEvents[i].StreamID != b.ExtraEvents[i].StreamID ||
			!bytes.Equal(a.ExtraEvents[i].Data, b.ExtraEvents[i].Data) {
			return false
		}
	}
	return true
}

func (a *App) reseedTunnelSnapshotAfterAttach(broker *tunnel.Broker, seeded tunnel.BrokerSnapshot) {
	if broker == nil {
		return
	}
	latest := a.tunnelSnapshot()
	if desktopTunnelSnapshotMatches(seeded, latest) {
		return
	}
	if a.agentBridge != nil {
		if current := a.agentBridge.CurrentSession(); current != nil && current.ID != "" {
			broker.SwitchSession(current.ID)
		} else {
			broker.ResetSession()
		}
	} else {
		broker.ResetSession()
	}
	broker.SendSnapshot(latest)
}

func (a *App) currentTunnelAgentSnapshotEvents() []tunnel.SnapshotEvent {
	if a.agentBridge == nil {
		return nil
	}
	panels := append([]AgentPanelData{}, a.agentBridge.SubAgentPanels()...)
	panels = append(panels, a.agentBridge.SwarmPanels()...)
	sort.Slice(panels, func(i, j int) bool { return panels[i].ID < panels[j].ID })
	out := make([]tunnel.SnapshotEvent, 0)
	for _, panel := range panels {
		if panel.ID == "" {
			continue
		}
		task := panel.Task
		if task == "" && panel.Kind == "teammate" {
			task = "teammate"
		}
		out = append(out, desktopSnapshotEvent(
			tunnel.EventSubagentSpawn,
			panel.ID,
			tunnel.SubagentSpawnData{AgentID: panel.ID, Name: panel.Name, Task: task, ParentID: panel.TeamID},
		))
		textID := "sa-" + panel.ID
		if panel.Kind == "teammate" {
			textID = "tm-" + panel.ID
		}
		textBuf := strings.Builder{}
		toolArgsByID := make(map[string]string)
		flushText := func(done bool) {
			if textBuf.Len() == 0 {
				return
			}
			out = append(out, desktopSnapshotEvent(
				tunnel.EventSubagentText,
				panel.ID,
				tunnel.SubagentTextData{AgentID: panel.ID, ID: textID, Chunk: textBuf.String(), Done: done},
			))
			textBuf.Reset()
		}
		for _, ev := range panel.Events {
			switch ev.Type {
			case "tool_call":
				flushText(false)
				if ev.ToolID != "" {
					toolArgsByID[ev.ToolID] = ev.ToolArgs
				}
				displayName := ev.ToolDisplayName
				if displayName == "" {
					displayName = toolDisplayName(ev.ToolName, ev.ToolArgs)
				}
				detail := ev.ToolDetail
				if detail == "" {
					detail = ev.Content
				}
				if detail == "" {
					detail = toolArgSummary(ev.ToolName, ev.ToolArgs)
				}
				out = append(out, desktopSnapshotEvent(
					tunnel.EventSubagentToolCall,
					panel.ID,
					tunnel.SubagentToolCallData{
						AgentID:     panel.ID,
						ToolID:      ev.ToolID,
						ToolName:    ev.ToolName,
						DisplayName: displayName,
						Args:        ev.ToolArgs,
						Detail:      detail,
					},
				))
			case "tool_result":
				flushText(false)
				rawArgs := ev.ToolArgs
				if rawArgs == "" {
					rawArgs = toolArgsByID[ev.ToolID]
				}
				present, _ := tool.DescribeToolResult(ev.ToolName, rawArgs, ev.Content, ev.IsError)
				delete(toolArgsByID, ev.ToolID)
				out = append(out, desktopSnapshotEvent(
					tunnel.EventSubagentToolResult,
					panel.ID,
					tunnel.SubagentToolResultData{
						AgentID:     panel.ID,
						ToolID:      ev.ToolID,
						ToolName:    ev.ToolName,
						DisplayName: ev.ToolDisplayName,
						Detail:      ev.ToolDetail,
						Result:      ev.Content,
						Summary:     present.Summary,
						Payload:     present.Payload,
						PayloadMode: present.PayloadMode,
						IsError:     ev.IsError,
					},
				))
			default:
				if ev.Content != "" {
					if ev.Type == "error" && textBuf.Len() > 0 {
						textBuf.WriteString("\n")
					}
					textBuf.WriteString(ev.Content)
				}
			}
		}
		if textBuf.Len() == 0 {
			switch {
			case panel.Result != "":
				textBuf.WriteString(panel.Result)
			case panel.Error != "":
				textBuf.WriteString(panel.Error)
			}
		}
		completed := panel.Status == "completed" || panel.Status == "failed" || panel.Status == "idle"
		flushText(completed)
		if completed {
			summary := panel.Result
			success := panel.Error == ""
			if summary == "" {
				summary = panel.Error
			}
			if summary == "" {
				summary = panel.Status
			}
			out = append(out, desktopSnapshotEvent(
				tunnel.EventSubagentComplete,
				panel.ID,
				tunnel.SubagentCompleteData{AgentID: panel.ID, Name: panel.Name, Summary: summary, Success: success},
			))
			continue
		}
		statusMessage := panel.Task
		if statusMessage == "" {
			statusMessage = panel.Name
		}
		out = append(out, desktopSnapshotEvent(
			tunnel.EventSubagentStatus,
			panel.ID,
			tunnel.SubagentStatusData{AgentID: panel.ID, Status: tunnel.StatusRunning, Message: statusMessage},
		))
	}
	return out
}

func desktopSnapshotEvent(eventType, streamID string, data interface{}) tunnel.SnapshotEvent {
	raw, _ := json.Marshal(data)
	return tunnel.SnapshotEvent{
		Type:     eventType,
		StreamID: streamID,
		Data:     raw,
	}
}

func (a *App) showTunnelInfo(info *tunnel.SessionInfo) {
	urlLabel := widget.NewEntry()
	urlLabel.SetText(info.ConnectURL)
	urlLabel.Wrapping = fyne.TextWrapOff
	urlLabel.SetPlaceHolder("Tunnel URL")

	copyBtn := widget.NewButton(t("share.copy_url"), func() {
		a.window.Clipboard().SetContent(info.ConnectURL)
		a.shareDialog.Hide()
	})

	stopBtn := widget.NewButton(t("share.stop"), func() {
		// Disconnect agent bridge from broker FIRST
		if a.agentBridge != nil {
			a.agentBridge.DetachTunnelBroker()
		}
		a.closeTunnelGracefully(2 * time.Second)
		a.shareDialog.Hide()
	})

	// Build QR code image
	var qrImage fyne.CanvasObject
	if len(info.QRCodePNG) > 0 {
		img := canvas.NewImageFromResource(fyne.NewStaticResource("qr.png", info.QRCodePNG))
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(256, 256))
		qrImage = container.NewCenter(img)
	} else {
		qrImage = widget.NewLabel(t("share.qr_unavailable"))
	}

	// Mobile app download links
	getAppLabel := widget.NewLabelWithStyle(t("share.get_app"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	iosLink := widget.NewHyperlink("iOS (TestFlight)", mustParseURL("https://testflight.apple.com/join/J34wVD6p"))
	androidLink := widget.NewHyperlink("Android (Closed Testing)", mustParseURL("https://play.google.com/apps/testing/gg.ai.ggcode.mobile"))
	discordLink := widget.NewHyperlink("Discord", mustParseURL("https://discord.gg/F2v4mJmfG"))

	items := []fyne.CanvasObject{
		widget.NewLabelWithStyle(t("share.mobile_connection"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel(t("share.scan_hint")),
	}
	if info.CompatibilityNotice != "" {
		items = append(items, widget.NewRichTextFromMarkdown("**Notice:** "+info.CompatibilityNotice))
	}
	items = append(items,
		urlLabel,
		container.NewHBox(copyBtn, stopBtn),
		widget.NewSeparator(),
		qrImage,
		widget.NewSeparator(),
		getAppLabel,
		container.NewHBox(
			widget.NewLabel("  "),
			container.NewVBox(iosLink, androidLink, discordLink),
		),
	)
	content := container.NewVBox(items...)

	a.shareDialog = dialog.NewCustom(t("share.title"), t("share.close"), content, a.window)
	a.shareDialog.Show()
}

func (a *App) closeTunnelGracefully(timeout time.Duration) {
	broker := a.currentTunnelBroker()
	sess := a.currentTunnelSession()
	if broker != nil {
		broker.StopSharingGracefully(timeout)
	} else if sess != nil {
		sess.DestroyGracefully(timeout)
	}
	a.clearTunnelState()
}

// openUpdates checks for the latest release on GitHub.
func (a *App) openUpdates() {
	go func() {
		latest, err := fetchLatestReleaseTag()
		if err != nil {
			fyne.Do(func() {
				dialog.ShowInformation(t("update.title"), t("update.check_failed", err.Error()), a.window)
			})
			return
		}
		msg := t("update.current_latest", Version, latest)
		if latest == Version || latest == "v"+Version {
			msg += "\n\n" + t("update.up_to_date")
		} else {
			msg += "\n\n" + t("update.available")
		}
		fyne.Do(func() {
			if latest == Version || latest == "v"+Version {
				dialog.ShowInformation(t("update.title"), msg, a.window)
			} else {
				dialog.ShowConfirm(t("update.available_title"), msg+"\n\n"+t("update.download_now"), func(ok bool) {
					if ok {
						a.fyneApp.OpenURL(mustParseURL("https://github.com/topcheer/ggcode/releases/latest"))
					}
				}, a.window)
			}
		})
	}()
	dialog.ShowInformation(t("update.title"), t("update.checking"), a.window)
}

// fetchLatestReleaseTag queries GitHub API for the latest release tag.
func fetchLatestReleaseTag() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/topcheer/ggcode/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

// mustParseURL parses a URL string, panicking on failure.
func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// ── Welcome screen ───────────────────────────────────

func (a *App) showWelcome() {
	title := canvas.NewText(t("app.welcome_title"), theme.ForegroundColor())
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}

	subtitle := widget.NewLabel(t("folder.select_title"))
	subtitle.Alignment = fyne.TextAlignCenter

	btn := widget.NewButtonWithIcon(t("folder.choose_directory"), theme.FolderOpenIcon(), func() {
		a.showFolderPicker()
	})
	btn.Importance = widget.HighImportance

	recentLabel := widget.NewLabel("")
	if a.dc.WorkDir != "" {
		recentLabel.SetText(t("folder.last_used", a.dc.WorkDir))
	}

	welcomeContent := container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(title),
		container.NewCenter(subtitle),
		container.NewCenter(btn),
		container.NewCenter(recentLabel),
		layout.NewSpacer(),
	)

	a.content.Objects = []fyne.CanvasObject{welcomeContent}
	a.content.Refresh()
	a.ui.SetStatus(t("status.select_project"))
	a.setTitle(t("app.title.welcome"))
}

func (a *App) showFolderPicker() {
	d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil {
			return
		}
		path := u.Path()
		if path == "" {
			path = filepath.Join(u.String())
		}
		if path != "" {
			a.initFromWorkDir(path)
		}
	}, a.window)
	d.Resize(fyne.NewSize(900, 600))
	d.Show()
}

// ── Init from workDir ────────────────────────────────

func (a *App) initFromWorkDir(dir string) {
	defer safeRecover("initFromWorkDir")

	// Set process working directory to the selected workspace so that all
	// file tools (read_file, write_file, edit_file, glob, etc.) resolve
	// relative paths correctly. In TUI/daemon mode this is naturally the
	// cwd, but GUI apps launch from a different directory.
	_ = os.Chdir(dir)

	a.ui.SetStatus(t("status.loading_workspace", dir))
	a.setTitle(fmt.Sprintf("ggcode — %s", filepath.Base(dir)))

	cfgPath := resolveConfigFilePath(dir)
	cfg, err := config.LoadWithInstance(cfgPath, dir)
	if err != nil {
		a.showError(t("error.load_config", err))
		return
	}
	a.cfg = cfg
	language := a.dc.Language
	if cfg.Language != "" {
		language = cfg.Language
	}
	setLanguage(language)
	a.dc.Language = normalizeLanguage(language)
	a.dc.WorkDir = dir
	a.initIMRuntime()
	_ = a.dc.Save()

	if cfg.NeedsOnboard() {
		a.showOnboard()
		return
	}
	a.startChat()
}

// ── Error display ────────────────────────────────────

func (a *App) showError(msg string) {
	errLabel := widget.NewLabel(msg)
	errLabel.Wrapping = fyne.TextWrapWord
	retryBtn := widget.NewButton(t("folder.retry"), func() { a.showFolderPicker() })

	card := widget.NewCard(t("status.error"), "", container.NewVBox(
		container.NewHBox(widget.NewIcon(theme.ErrorIcon()), errLabel),
		retryBtn,
	))

	a.content.Objects = []fyne.CanvasObject{card}
	a.content.Refresh()
	a.ui.SetStatus(t("status.error"))
}

// ── Onboard wizard ────────────────────────────────────

func (a *App) showOnboard() {
	presets := config.VendorPresets()
	if len(presets) == 0 {
		a.showError(t("onboard.no_presets"))
		return
	}

	vendorNames := make([]string, len(presets))
	for i, p := range presets {
		vendorNames[i] = p.DisplayName
	}

	vendorSelect := widget.NewSelect(vendorNames, nil)
	vendorSelect.PlaceHolder = t("onboard.choose_vendor")
	endpointSelect := widget.NewSelect([]string{}, nil)
	endpointSelect.PlaceHolder = t("sidebar.endpoint_label")
	apiEntry := widget.NewPasswordEntry()
	apiEntry.PlaceHolder = t("onboard.enter_api_key")
	modelSelect := widget.NewSelect([]string{}, nil)
	modelSelect.PlaceHolder = t("onboard.select_model")

	var selectedPreset *config.VendorPreset
	var selectedEndpoint *config.EndpointPreset

	endpointLabel := func(ep config.EndpointPreset) string {
		if strings.TrimSpace(ep.DisplayName) != "" {
			return ep.DisplayName
		}
		return ep.ID
	}

	findSelectedEndpoint := func(name string) *config.EndpointPreset {
		if selectedPreset == nil {
			return nil
		}
		for i := range selectedPreset.Endpoints {
			if endpointLabel(selectedPreset.Endpoints[i]) == name {
				return &selectedPreset.Endpoints[i]
			}
		}
		return nil
	}

	updateModelOptions := func() {
		modelSelect.ClearSelected()
		modelSelect.Options = nil
		if selectedPreset == nil || selectedEndpoint == nil {
			modelSelect.Refresh()
			return
		}
		a.cfg.Vendor = selectedPreset.ID
		a.cfg.Endpoint = selectedEndpoint.ID
		if apiEntry.Text == "" {
			modelSelect.Options = []string{t("onboard.enter_api_key_first")}
			modelSelect.Refresh()
			return
		}
		a.cfg.SetEndpointAPIKey(selectedPreset.ID, selectedEndpoint.ID, apiEntry.Text, false)
		resolved, err := a.cfg.ResolveActiveEndpoint()
		if err != nil {
			modelSelect.Options = []string{t("onboard.error_option")}
			modelSelect.Refresh()
			return
		}
		models := resolved.Models
		if len(models) == 0 && resolved.Model != "" {
			models = []string{resolved.Model}
		}
		modelSelect.Options = models
		modelSelect.Refresh()
		if len(models) == 0 {
			return
		}
		selectedModel := resolved.Model
		if selectedModel == "" {
			selectedModel = models[0]
		}
		modelSelect.SetSelected(selectedModel)
	}

	updateEndpointOptions := func() {
		selectedEndpoint = nil
		endpointSelect.ClearSelected()
		endpointSelect.Options = nil
		if selectedPreset == nil {
			endpointSelect.Refresh()
			updateModelOptions()
			return
		}

		options := make([]string, 0, len(selectedPreset.Endpoints))
		defaultSelection := ""
		for _, ep := range selectedPreset.Endpoints {
			label := endpointLabel(ep)
			options = append(options, label)
			if defaultSelection == "" || ep.ID == selectedPreset.DefaultEndpoint {
				defaultSelection = label
			}
		}
		endpointSelect.Options = options
		endpointSelect.Refresh()
		if defaultSelection != "" {
			endpointSelect.SetSelected(defaultSelection)
			return
		}
		updateModelOptions()
	}

	vendorSelect.OnChanged = func(name string) {
		selectedPreset = nil
		for i := range presets {
			if presets[i].DisplayName == name {
				selectedPreset = &presets[i]
				break
			}
		}
		if selectedPreset == nil {
			updateEndpointOptions()
			return
		}
		a.cfg.Vendor = selectedPreset.ID
		updateEndpointOptions()
	}

	endpointSelect.OnChanged = func(name string) {
		selectedEndpoint = findSelectedEndpoint(name)
		if selectedPreset != nil && selectedEndpoint != nil {
			a.cfg.Vendor = selectedPreset.ID
			a.cfg.Endpoint = selectedEndpoint.ID
		}
		updateModelOptions()
	}

	form := &widget.Form{
		Items: []*widget.FormItem{
			{Text: t("sidebar.vendor_label"), Widget: vendorSelect},
			{Text: t("sidebar.endpoint_label"), Widget: endpointSelect},
			{Text: t("sidebar.api_key_label"), Widget: apiEntry},
			{Text: t("sidebar.model_label"), Widget: modelSelect},
		},
		OnSubmit: func() {
			if selectedPreset == nil || selectedEndpoint == nil || apiEntry.Text == "" || modelSelect.Selected == "" {
				dialog.ShowInformation(t("onboard.missing_fields_title"), t("onboard.missing_fields_message"), a.window)
				return
			}
			a.cfg.Vendor = selectedPreset.ID
			a.cfg.Endpoint = selectedEndpoint.ID
			a.cfg.Model = modelSelect.Selected
			a.cfg.SetEndpointAPIKey(selectedPreset.ID, selectedEndpoint.ID, apiEntry.Text, true)
			if err := a.cfg.Save(); err != nil {
				a.showError(t("error.save_config", err))
				return
			}
			a.startChat()
		},
		OnCancel:   func() { a.showWelcome() },
		SubmitText: t("onboard.start"),
		CancelText: t("onboard.back"),
	}

	card := widget.NewCard(t("onboard.card_title"), t("onboard.card_subtitle"), form)
	panelSizer := canvas.NewRectangle(color.Transparent)
	panelSizer.SetMinSize(onboardPanelMinSize)
	panel := container.NewStack(panelSizer, card)
	a.content.Objects = []fyne.CanvasObject{container.NewCenter(panel)}
	a.content.Refresh()
	a.ui.SetStatus(t("onboard.setup_required"))
}

// ── Chat ─────────────────────────────────────────────

func (a *App) resumeSession(id string) {
	defer safeRecover("resumeSession")

	// Prevent double-clicks while loading.
	if a.sessionLoading {
		return
	}
	a.sessionLoading = true

	// Clear current chat and show loading indicator immediately.
	if a.chatViewRef != nil {
		a.chatViewRef.vbox.Objects = nil
		a.chatViewRef.msgWidgets = nil
		a.chatViewRef.toolWidgets = make(map[string]*toolWidgetRef)
		a.chatViewRef.streamW = nil
		a.chatViewRef.hideThinking()
		a.chatViewRef.showSessionLoading()
	}
	// Update status bar via binding.
	_ = a.ui.StatusBarStatus.Set(t("status.loading_session"))

	// Run the heavy work in a goroutine so the UI stays responsive.
	go func() {
		defer func() {
			a.sessionLoading = false
			safeRecover("resumeSession-goroutine")
		}()

		// Cancel current work if busy.
		if a.agentBridge != nil {
			a.agentBridge.Cancel()
		}

		// Load session and restore messages into agent.
		if a.agentBridge != nil {
			if err := a.agentBridge.ResumeSession(id); err != nil {
				fyne.Do(func() {
					if a.chatViewRef != nil {
						a.chatViewRef.hideSessionLoading()
					}
					a.showError(t("error.resume_session", err))
				})
				return
			}
		}

		// Rebuild chat view from session messages (must run on UI thread).
		fyne.Do(func() {
			if a.chatViewRef != nil {
				a.chatViewRef.hideSessionLoading()
			}
			if a.chatViewRef != nil && a.agentBridge != nil && a.agentBridge.CurrentSession() != nil {
				a.chatViewRef.rebuildFromMessages(a.agentBridge.CurrentSession().Messages)
			}

			// Update status bar.
			_ = a.ui.StatusBarStatus.Set(t("status.ready"))
		})

		// Push updated session info + history to mobile client.
		if broker := a.currentTunnelBroker(); broker != nil && a.agentBridge != nil && a.agentBridge.CurrentSession() != nil {
			broker.SwitchSession(a.agentBridge.CurrentSession().ID)
			a.agentBridge.ResetCurrentSessionTunnelLedger()
			broker.SendSnapshot(a.tunnelSnapshot())
		}

		// Refresh sidebar.
		if a.sidebarRef != nil {
			fyne.Do(func() {
				a.sidebarRef.loadSessions()
				a.sidebarRef.sessionList.Refresh()
			})
		}
	}()
}

func (a *App) newSession() {
	defer safeRecover("newSession")

	// 1. Detach broker from bridge FIRST so Cancel() won't enqueue messages.
	if a.agentBridge != nil {
		a.agentBridge.DetachTunnelBroker()
	}

	// 2. Cancel current work (won't push to broker now).
	if a.agentBridge != nil {
		a.agentBridge.Cancel()
	}

	// 3. Save current session.
	if a.agentBridge != nil {
		a.agentBridge.saveSession()
	}

	// 4. Clear session ID so startChat creates and publishes a fresh active session.
	if a.agentBridge != nil {
		a.agentBridge.ClearCurrentSession()
	}

	// Clear chat view immediately (lightweight — just clear widgets).
	if a.chatViewRef != nil {
		a.chatViewRef.vbox.Objects = nil
		a.chatViewRef.vbox.Refresh()
		a.chatViewRef.msgWidgets = nil
		a.chatViewRef.toolWidgets = make(map[string]*toolWidgetRef)
		a.chatViewRef.streamW = nil
	}

	a.startChat()
}

func (a *App) startChat() {
	defer safeRecover("startChat")

	// Stop previous statusLoop if any.
	if a.chatViewRef != nil && a.chatViewRef.stopCh != nil {
		close(a.chatViewRef.stopCh)
		a.chatViewRef.stopCh = nil

		// Stop old IM adapters.
		a.stopIMAdapters()
	}

	// Save current session before switching.
	var prevSessionID string
	if a.agentBridge != nil {
		a.agentBridge.saveSession()
		if a.agentBridge.CurrentSession() != nil {
			prevSessionID = a.agentBridge.CurrentSession().ID
		}
	}

	resolved, err := a.cfg.ResolveActiveEndpoint()
	if err != nil {
		a.showError(t("error.resolve_endpoint", err))
		return
	}

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		a.showError(t("error.create_provider", err))
		return
	}

	bridge := NewAgentBridge(a.cfg, prov, resolved, a.dc.WorkDir, a.ui)
	a.agentBridge = bridge
	bridge.SetMainWindow(a.window)

	// Set IM emitter for outbound message push.
	if a.imManager != nil {
		bridge.Emitter = im.NewIMEmitter(a.imManager, a.cfg.Language, a.dc.WorkDir)
	}

	// Resume previous session into new bridge to preserve history.
	if prevSessionID != "" {
		_ = bridge.ResumeSession(prevSessionID)
	}
	if bridge.CurrentSession() == nil {
		bridge.ensureSession()
	}
	if broker := a.currentTunnelBroker(); broker != nil {
		bridge.AttachTunnelBroker(broker)
		if current := bridge.CurrentSession(); current != nil {
			broker.SwitchSession(current.ID)
			bridge.ResetCurrentSessionTunnelLedger()
			broker.SendSnapshot(a.tunnelSnapshot())
		}
	}

	// Create or reuse chat view and sidebar.
	// Reuse existing widgets to avoid expensive UI rebuild on new-session.
	if a.chatViewRef == nil {
		cv := NewChatView(a, bridge, a.ui)
		a.chatViewRef = cv
		sb := NewSidebar(a, bridge, a.ui)
		a.sidebarRef = sb
		sidebarObj := sb.Render()
		chatViewObj := cv.Render()

		split := container.NewHSplit(chatViewObj, sidebarObj)
		split.SetOffset(0.75)
		a.split = split
		a.chatViewObj = chatViewObj
		a.sidebarObj = sidebarObj
		a.sidebarHidden = false
		a.content.Objects = []fyne.CanvasObject{split}
		a.content.Refresh()
	} else {
		// Reuse existing widgets — just update the bridge reference.
		a.chatViewRef.bridge = bridge
		a.chatViewRef.ui = a.ui
		if a.sidebarRef != nil {
			a.sidebarRef.bridge = bridge
			a.sidebarRef.ui = a.ui
			a.sidebarRef.loadSessions()
			a.sidebarRef.sessionList.Refresh()
		}
	}

	// Restore chat history from resumed session.
	if a.chatViewRef != nil && bridge.CurrentSession() != nil && len(bridge.CurrentSession().Messages) > 0 {
		a.chatViewRef.rebuildFromMessages(bridge.CurrentSession().Messages)
	}

	a.ui.SetModelInfo(resolved.Model, humanizeTokens(resolved.ContextWindow))
	_ = a.ui.StatusBarVendor.Set(fmt.Sprintf("%s/%s", resolved.VendorID, resolved.Model))
	a.ui.SetStatus(fmt.Sprintf("%s/%s | context %s",
		resolved.VendorID, resolved.Model, humanizeTokens(resolved.ContextWindow)))
	a.setTitle(fmt.Sprintf("ggcode — %s [%s]", filepath.Base(a.dc.WorkDir), resolved.Model))

	// Register global keyboard shortcuts.
	a.registerShortcuts()

}

func (a *App) refreshSidebar() {
	if a.agentBridge != nil {
		tc := a.agentBridge.TokenCount()
		cw := a.agentBridge.ContextWindow()
		a.ui.SetTokenUsage(fmt.Sprintf("%s / %s", humanizeTokens(tc), humanizeTokens(cw)), float64(tc)/float64(max(cw, 1)))
	}
}

func (a *App) applyLanguageChange(language string) {
	language = normalizeLanguage(language)
	setLanguage(language)
	if a.cfg != nil {
		a.cfg.Language = language
		_ = a.cfg.SaveLanguagePreference(language)
	}
	a.dc.Language = language
	_ = a.dc.Save()
	if broker := a.currentTunnelBroker(); broker != nil {
		broker.SendLanguageChange(language)
	}
	if a.agentBridge != nil && a.imManager != nil && a.dc != nil {
		a.agentBridge.Emitter = im.NewIMEmitter(a.imManager, language, a.dc.WorkDir)
	}
	fyne.Do(func() {
		a.refreshLanguageUI()
	})
}

func (a *App) refreshLanguageUI() {
	a.setupMenu()
	if a.agentBridge == nil {
		if a.cfg != nil && a.cfg.NeedsOnboard() {
			a.showOnboard()
			return
		}
		if a.dc != nil && a.dc.WorkDir == "" {
			a.showWelcome()
			return
		}
	}
	if a.imWindow != nil {
		a.imWindow.SetTitle(t("im.title"))
		a.refreshIMWindow()
	}
	if a.sidebarRef != nil && a.agentBridge != nil {
		selectedTab := 0
		offset := 0.75
		if a.sidebarRef.tabs != nil {
			selectedTab = a.sidebarRef.tabs.SelectedIndex()
		}
		if a.split != nil {
			offset = a.split.Offset
		}
		sb := NewSidebar(a, a.agentBridge, a.ui)
		sidebarObj := sb.Render()
		if sb.tabs != nil && selectedTab >= 0 && selectedTab < len(sb.tabs.Items) {
			sb.tabs.SelectIndex(selectedTab)
		}
		a.sidebarRef = sb
		a.sidebarObj = sidebarObj
		if a.split != nil {
			a.split.Trailing = sidebarObj
			a.split.SetOffset(offset)
		}
		if a.sidebarHidden {
			a.content.Objects = []fyne.CanvasObject{a.chatViewObj}
		} else if a.split != nil {
			a.content.Objects = []fyne.CanvasObject{a.split}
		}
		a.content.Refresh()
	}
}

// applyThemeChange switches the desktop theme at runtime.
func (a *App) applyThemeChange(themeName string) {
	themeName = normalizeThemeName(themeName)
	a.dc.Theme = themeName
	a.dc.Save()
	// Notify mobile clients
	if broker := a.currentTunnelBroker(); broker != nil {
		broker.SendThemeChange(themeName)
	}
	fyne.Do(func() {
		a.fyneApp.Settings().SetTheme(newThemeForScheme(themeName))
		a.setupMenu()
		a.refreshNativeTitlebar()
		for _, win := range a.fyneApp.Driver().AllWindows() {
			if content := win.Content(); content != nil {
				content.Refresh()
			}
		}
	})
}

// buildThemeMenu creates the Theme submenu with check marks.
func (a *App) buildThemeMenu() *fyne.MenuItem {
	themeLabels := map[string]string{
		"midnight": "Midnight",
		"oled":     "OLED Black",
		"nord":     "Nord",
		"rose":     "Rose",
		"forest":   "Forest",
		"light":    "Light",
	}
	items := make([]*fyne.MenuItem, 0, len(availableThemes))
	for _, name := range availableThemes {
		label := themeLabels[name]
		item := fyne.NewMenuItem(label, func() {
			a.applyThemeChange(name)
		})
		if name == normalizeThemeName(a.dc.Theme) {
			item.Checked = true
		}
		items = append(items, item)
	}
	themeMenu := fyne.NewMenuItem(t("menu.theme"), nil)
	themeMenu.ChildMenu = fyne.NewMenu("", items...)
	return themeMenu
}

// buildLanguageMenu creates the Language submenu with check marks.
func (a *App) buildLanguageMenu() *fyne.MenuItem {
	current := normalizeLanguage(a.dc.Language)
	items := []*fyne.MenuItem{
		fyne.NewMenuItem(t("menu.language.english"), func() {
			a.applyLanguageChange("en")
		}),
		fyne.NewMenuItem(t("menu.language.chinese_simplified"), func() {
			a.applyLanguageChange("zh-CN")
		}),
	}
	items[0].Checked = current == "en"
	items[1].Checked = current == "zh-CN"
	languageMenu := fyne.NewMenuItem(t("menu.language"), nil)
	languageMenu.ChildMenu = fyne.NewMenu("", items...)
	return languageMenu
}

// ── Helpers ──────────────────────────────────────────

func (a *App) setTitle(title string) {
	a.windowTitle = title
	a.window.SetTitle(title)
	if a.titleBarLabel != nil {
		a.titleBarLabel.SetText(title)
		a.titleBarLabel.Refresh()
	}
	a.refreshNativeTitlebar()
}

func (a *App) toggleSidebar() {
	a.sidebarHidden = !a.sidebarHidden
	if a.sidebarHidden {
		a.content.Objects = []fyne.CanvasObject{a.chatViewObj}
	} else {
		a.content.Objects = []fyne.CanvasObject{a.split}
	}
	a.content.Refresh()
}

// showFilePreview opens a file preview in the main content area.
func (a *App) showFilePreview(filePath string, targetLine int) {
	if a.content == nil {
		return
	}
	fp := NewFilePreview(a, filePath, targetLine, func() {
		a.closeFilePreview()
	})
	a.filePreview = fp

	// Show preview: replace left side of the existing split, keep sidebar ratio
	if a.sidebarHidden {
		a.content.Objects = []fyne.CanvasObject{fp.Widget()}
	} else {
		a.content.Objects = []fyne.CanvasObject{a.split}
		a.split.Leading = fp.Widget()
	}
	a.content.Refresh()
}

// closeFilePreview restores the chat view.
func (a *App) closeFilePreview() {
	if a.filePreview != nil {
		a.filePreview.Close()
	}
	a.filePreview = nil
	if a.sidebarHidden {
		a.content.Objects = []fyne.CanvasObject{a.chatViewObj}
	} else {
		a.split.Leading = a.chatViewObj
		a.content.Objects = []fyne.CanvasObject{a.split}
	}
	a.content.Refresh()
}

func (a *App) registerShortcuts() {
	if a.window == nil || a.window.Canvas() == nil {
		return
	}
	c := a.window.Canvas()

	// Ctrl+B / Cmd+B: Toggle sidebar
	c.AddShortcut(&desktop.CustomShortcut{
		KeyName:  fyne.KeyB,
		Modifier: fyne.KeyModifierShortcutDefault,
	}, func(fyne.Shortcut) {
		a.toggleSidebar()
	})

	// Escape: Close file preview (if open)
	c.AddShortcut(&desktop.CustomShortcut{
		KeyName: fyne.KeyEscape,
	}, func(fyne.Shortcut) {
		if a.filePreview != nil {
			a.closeFilePreview()
		}
	})
}

func resolveConfigFilePath(workDir string) string {
	for _, p := range []string{
		filepath.Join(workDir, "ggcode.yaml"),
		filepath.Join(workDir, ".ggcode", "ggcode.yaml"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return config.ConfigPath()
}

func humanizeTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// initIMRuntime initializes the IM manager once at app startup.
func (a *App) initIMRuntime() {
	if a.imManager != nil {
		return
	}
	defer safeRecover("initIMRuntime")

	mgr := im.NewManager()

	bindingsPath, err := im.DefaultBindingsPath()
	if err != nil {
		return
	}
	bindingStore, err := im.NewJSONFileBindingStore(bindingsPath)
	if err != nil {
		return
	}
	if err := mgr.SetBindingStore(bindingStore); err != nil {
		return
	}

	pairingPath, err := im.DefaultPairingStatePath()
	if err != nil {
		return
	}
	pairingStore, err := im.NewJSONFilePairingStore(pairingPath)
	if err != nil {
		return
	}
	if err := mgr.SetPairingStore(pairingStore); err != nil {
		return
	}

	workDir := ""
	if a.dc != nil {
		workDir = a.dc.WorkDir
	}
	mgr.BindSession(im.SessionBinding{Workspace: workDir})

	if a.cfg != nil {
		adapters := make(map[string]bool)
		for name, acfg := range a.cfg.IM.Adapters {
			adapters[name] = acfg.Enabled
		}
		mgr.ApplyAdapterConfig(adapters)
	}

	mgr.SetOnUpdate(func(snap im.StatusSnapshot) {
		if snap.PendingPairing != nil && a.imPairingWin == nil {
			ch := snap.PendingPairing
			fyne.Do(func() {
				a.showPairingCodeDialog(ch)
			})
		}
		if snap.PendingPairing == nil && a.imPairingWin != nil {
			fyne.Do(func() {
				a.imPairingWin.Close()
				a.imPairingWin = nil
			})
		}
	})

	a.imManager = mgr

	// Start adapters bound to current workspace.
	a.startIMAdapters()
}
