package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/topcheer/ggcode/internal/im"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// App is the top-level desktop application state.
type App struct {
	fyneApp fyne.App
	window  fyne.Window
	dc      *DesktopConfig
	cfg     *config.Config

	// IM runtime.
	imManager    *im.Manager
	imController *im.AdapterController
	imWindow     fyne.Window
	imPairingWin fyne.Window

	// Shared UI state for cross-goroutine updates.
	ui *UIState

	// UI components.
	content       *fyne.Container
	statusBar     *widget.Label
	split         *container.Split
	chatViewObj   fyne.CanvasObject
	chatViewRef   *ChatView
	sidebarObj    fyne.CanvasObject
	sidebarRef    *Sidebar
	sidebarHidden bool
	filePreview   *FilePreview // currently shown file preview, or nil

	// Agent state.
	agentBridge *AgentBridge

	// Mobile tunnel.
	tunnelSession *tunnel.Session
	tunnelBroker  *tunnel.Broker
	shareDialog   dialog.Dialog
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
	a.window = a.fyneApp.NewWindow("ggcode")
	setWindowIcon(a.window)
	a.buildUI()
	a.setupMenu()

	// Apply dark titlebar matching the app theme.
	setupNativeTitlebar(a.window)

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
		if a.tunnelSession != nil {
			a.tunnelSession.Stop()
		}
	})

	a.window.ShowAndRun()
}

// ── UI construction ──────────────────────────────────

func (a *App) buildUI() {
	// Status bar — updated directly by pollRefresh.
	a.statusBar = widget.NewLabel("Ready")
	a.statusBar.TextStyle = fyne.TextStyle{Monospace: true}
	a.ui.SetStatusLabel(a.statusBar)

	statusBox := container.NewHBox(
		a.statusBar,
		layout.NewSpacer(),
	)

	a.content = container.NewStack(widget.NewLabel(""))

	root := container.NewBorder(nil, statusBox, nil, nil, a.content)
	a.window.SetContent(root)
}

func (a *App) setupMenu() {
	fileMenu := fyne.NewMenu("File",
		fyne.NewMenuItem("Open Project...", func() { a.showFolderPicker() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { a.fyneApp.Quit() }),
	)
	viewMenu := fyne.NewMenu("View",
		fyne.NewMenuItem("Toggle Sidebar", func() { a.toggleSidebar() }),
		fyne.NewMenuItem("Refresh Stats", func() { a.refreshSidebar() }),
	)
	toolsMenu := fyne.NewMenu("Tools",
		fyne.NewMenuItem("IM Settings...", func() { a.showIMWindow() }),
	)
	helpMenu := fyne.NewMenu("Help",
		fyne.NewMenuItem("About", func() { a.showAbout() }),
		fyne.NewMenuItem("Check for Updates", func() { a.openUpdates() }),
	)
	a.window.SetMainMenu(fyne.NewMainMenu(fileMenu, viewMenu, toolsMenu, helpMenu))
}

// showAbout displays the About dialog with app icon, version, and links.
func (a *App) showAbout() {
	icon := canvas.NewImageFromResource(fyne.NewStaticResource("icon.png", iconBytes))
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(96, 96))

	title := widget.NewLabelWithStyle("ggcode", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	versionLabel := widget.NewLabelWithStyle("Version "+Version, fyne.TextAlignCenter, fyne.TextStyle{Monospace: true})
	desc := widget.NewLabel("AI-powered coding assistant\nwith IM integration")
	desc.Alignment = fyne.TextAlignCenter

	releaseLink := widget.NewHyperlink("GitHub Releases", mustParseURL("https://github.com/topcheer/ggcode/releases"))
	releaseLink.Alignment = fyne.TextAlignCenter
	issuesLink := widget.NewHyperlink("Report an Issue", mustParseURL("https://github.com/topcheer/ggcode/issues"))
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

	dialog.ShowCustom("About ggcode", "Close", content, a.window)
}

// showShareDialog starts a tunnel and shows the connection QR code + URL.
func (a *App) showShareDialog() {
	if a.tunnelSession != nil {
		// Already running — show current info
		info := a.tunnelSession.Info()
		if info != nil {
			a.showTunnelInfo(info)
		}
		return
	}

	// Show "connecting..." dialog
	statusLabel := widget.NewLabel("Establishing tunnel...")
	statusLabel.Alignment = fyne.TextAlignCenter
	progress := widget.NewProgressBarInfinite()
	connectContent := container.NewVBox(statusLabel, progress)
	connectWin := dialog.NewCustom("Share Session", "Cancel", connectContent, a.window)
	connectWin.Show()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sess := tunnel.NewSession(tunnel.DefaultRelayURL)
		info, err := sess.Start(ctx)
		if err != nil {
			fyne.Do(func() {
				connectWin.Hide()
				dialog.ShowError(fmt.Errorf("tunnel failed: %v", err), a.window)
			})
			return
		}

		broker := tunnel.NewBroker(sess)

		// Handle commands from mobile client
		broker.OnCommand(func(cmd tunnel.GatewayMessage) {
			var payload map[string]interface{}
			if len(cmd.Data) > 0 {
				json.Unmarshal(cmd.Data, &payload)
			}
			switch cmd.Type {
			case "user_text", "message":
				text, _ := payload["text"].(string)
				if text != "" {
					fyne.Do(func() {
						if a.ui != nil {
							a.ui.AppendChat(ChatMessage{Role: "user", Content: text, Time: time.Now()})
						}
					})
					if a.agentBridge != nil {
						a.agentBridge.Send(text)
					}
				}
			case "approval_result":
				// TODO: wire to permission handler
			case "ask_user_response":
				// TODO: wire to ask_user handler
			}
		})

		// When mobile client connects, send session_info to confirm connection
		broker.OnClientConnect(func() {
			// Replay everything the broker has sent.
			broker.ReplayToClient()

			// Show system message in desktop chat
			fyne.Do(func() {
				if a.ui != nil {
					a.ui.AppendChat(ChatMessage{Role: "system", Content: "Mobile client connected", Time: time.Now()})
				}
				// Hide share dialog
				if a.shareDialog != nil {
					a.shareDialog.Hide()
					a.shareDialog = nil
				}
			})
		})

		a.tunnelSession = sess
		a.tunnelBroker = broker
		if a.agentBridge != nil {
			a.agentBridge.tunnelBroker = broker

			// Seed sentLog with current session history so mobile
			// gets the full conversation on first connect.
			broker.SendSessionInfo(tunnel.SessionInfoData{
				Workspace: a.dc.WorkDir,
				Version:   Version,
			})
			session := a.agentBridge.CurrentSession()
			if session != nil && len(session.Messages) > 0 {
				history := make([]tunnel.HistoryEntry, 0, len(session.Messages)*2)
				for _, msg := range session.Messages {
					if msg.Role == "user" || msg.Role == "tool" {
						var textParts []string
						for _, block := range msg.Content {
							switch block.Type {
							case "text":
								if strings.TrimSpace(block.Text) != "" {
									textParts = append(textParts, strings.TrimSpace(block.Text))
								}
							case "tool_result":
								result := block.Output
								if len(result) > 500 {
									result = result[:500] + "..."
								}
								history = append(history, tunnel.HistoryEntry{
									Role:     "tool_result",
									ToolID:   block.ToolID,
									ToolName: block.ToolName,
									Result:   result,
									IsError:  block.IsError,
								})
							}
						}
						if len(textParts) > 0 {
							history = append(history, tunnel.HistoryEntry{
								Role:    "user",
								Content: strings.Join(textParts, "\n"),
							})
						}
					} else if msg.Role == "assistant" {
						for _, block := range msg.Content {
							switch block.Type {
							case "text":
								if strings.TrimSpace(block.Text) != "" {
									history = append(history, tunnel.HistoryEntry{
										Role:    "assistant",
										Content: strings.TrimSpace(block.Text),
									})
								}
							case "tool_use":
								detail := ""
								if block.Input != nil {
									var input map[string]interface{}
									if json.Unmarshal(block.Input, &input) == nil {
										if desc, ok := input["description"].(string); ok && desc != "" {
											detail = desc
										} else {
											for _, v := range input {
												if s, ok := v.(string); ok && s != "" {
													detail = s
													break
												}
											}
										}
									}
								}
								history = append(history, tunnel.HistoryEntry{
									Role:     "tool_call",
									ToolID:   block.ToolID,
									ToolName: block.ToolName,
									ToolArgs: detail,
								})
							}
						}
					}
				}
				if len(history) > 0 {
					broker.PushChatHistory(history)
				}
			}
			broker.PushStatus(tunnel.StatusIdle, "Ready")
		}

		fyne.Do(func() {
			connectWin.Hide()
			a.showTunnelInfo(info)
		})
	}()
}

func (a *App) showTunnelInfo(info *tunnel.SessionInfo) {
	urlLabel := widget.NewEntry()
	urlLabel.SetText(info.ConnectURL)
	urlLabel.Wrapping = fyne.TextWrapOff
	urlLabel.SetPlaceHolder("Tunnel URL")

	copyBtn := widget.NewButton("Copy URL", func() {
		a.window.Clipboard().SetContent(info.ConnectURL)
		a.shareDialog.Hide()
	})

	stopBtn := widget.NewButton("Stop Sharing", func() {
		// Disconnect agent bridge from broker FIRST
		if a.agentBridge != nil {
			a.agentBridge.tunnelBroker = nil
		}
		// Send sharing_stopped synchronously, THEN close the connection.
		if a.tunnelSession != nil {
			_ = a.tunnelSession.Send(tunnel.GatewayMessage{Type: "sharing_stopped"})
			a.tunnelSession.Stop()
			a.tunnelSession = nil
			a.tunnelBroker = nil
		}
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
		qrImage = widget.NewLabel("QR code unavailable")
	}

	content := container.NewVBox(
		widget.NewLabelWithStyle("Mobile Connection", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("Scan QR code in GGCode Mobile app, or copy URL:"),
		urlLabel,
		container.NewHBox(copyBtn, stopBtn),
		widget.NewSeparator(),
		qrImage,
	)

	a.shareDialog = dialog.NewCustom("Share Session", "Close", content, a.window)
	a.shareDialog.Show()
}

// openUpdates checks for the latest release on GitHub.
func (a *App) openUpdates() {
	go func() {
		latest, err := fetchLatestReleaseTag()
		if err != nil {
			fyne.Do(func() {
				dialog.ShowInformation("Update Check", "Could not check for updates:\n"+err.Error(), a.window)
			})
			return
		}
		msg := fmt.Sprintf("Current: %s\nLatest: %s", Version, latest)
		if latest == Version || latest == "v"+Version {
			msg += "\n\nYou are up to date!"
		} else {
			msg += "\n\nA newer version is available."
		}
		fyne.Do(func() {
			if latest == Version || latest == "v"+Version {
				dialog.ShowInformation("Update Check", msg, a.window)
			} else {
				dialog.ShowConfirm("Update Available", msg+"\n\nDownload now?", func(ok bool) {
					if ok {
						a.fyneApp.OpenURL(mustParseURL("https://github.com/topcheer/ggcode/releases/latest"))
					}
				}, a.window)
			}
		})
	}()
	dialog.ShowInformation("Update Check", "Checking for updates...", a.window)
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
	title := canvas.NewText("Welcome to ggcode", theme.ForegroundColor())
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}

	subtitle := widget.NewLabel("Select your project directory to get started.")
	subtitle.Alignment = fyne.TextAlignCenter

	btn := widget.NewButtonWithIcon("Choose Directory", theme.FolderOpenIcon(), func() {
		a.showFolderPicker()
	})
	btn.Importance = widget.HighImportance

	recentLabel := widget.NewLabel("")
	if a.dc.WorkDir != "" {
		recentLabel.SetText("Last used: " + a.dc.WorkDir)
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
	a.ui.SetStatus("Select a project directory")
	a.setTitle("ggcode — Welcome")
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

	a.ui.SetStatus(fmt.Sprintf("Loading %s...", dir))
	a.window.SetTitle(fmt.Sprintf("ggcode — %s", filepath.Base(dir)))

	cfgPath := resolveConfigFilePath(dir)
	cfg, err := config.LoadWithInstance(cfgPath, dir)
	if err != nil {
		a.showError(fmt.Sprintf("Failed to load config: %v", err))
		return
	}
	a.cfg = cfg
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
	retryBtn := widget.NewButton("Choose Another Directory", func() { a.showFolderPicker() })

	card := widget.NewCard("Error", "", container.NewVBox(
		container.NewHBox(widget.NewIcon(theme.ErrorIcon()), errLabel),
		retryBtn,
	))

	a.content.Objects = []fyne.CanvasObject{card}
	a.content.Refresh()
	a.ui.SetStatus("Error")
}

// ── Onboard wizard ────────────────────────────────────

func (a *App) showOnboard() {
	presets := config.VendorPresets()
	if len(presets) == 0 {
		a.showError("No vendor presets available. Please configure manually in ggcode.yaml.")
		return
	}

	vendorNames := make([]string, len(presets))
	for i, p := range presets {
		vendorNames[i] = p.DisplayName
	}

	vendorSelect := widget.NewSelect(vendorNames, nil)
	vendorSelect.PlaceHolder = "Choose a vendor..."
	apiEntry := widget.NewPasswordEntry()
	apiEntry.PlaceHolder = "Enter API key..."
	modelSelect := widget.NewSelect([]string{}, nil)
	modelSelect.PlaceHolder = "Select model..."

	var selectedPreset *config.VendorPreset

	vendorSelect.OnChanged = func(name string) {
		for i := range presets {
			if presets[i].DisplayName == name {
				selectedPreset = &presets[i]
				break
			}
		}
		if selectedPreset == nil {
			return
		}
		epID := selectedPreset.DefaultEndpoint
		if len(selectedPreset.Endpoints) > 0 {
			epID = selectedPreset.Endpoints[0].ID
		}
		a.cfg.Vendor = selectedPreset.ID
		a.cfg.Endpoint = epID
		if apiEntry.Text != "" {
			a.cfg.SetEndpointAPIKey(selectedPreset.ID, epID, apiEntry.Text, false)
			resolved, err := a.cfg.ResolveActiveEndpoint()
			if err != nil {
				modelSelect.Options = []string{"(error)"}
				modelSelect.Refresh()
				return
			}
			models := resolved.Models
			if len(models) == 0 {
				models = []string{resolved.Model}
			}
			modelSelect.Options = models
		} else {
			modelSelect.Options = []string{"(enter API key first)"}
		}
		modelSelect.Refresh()
	}

	form := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "Vendor", Widget: vendorSelect},
			{Text: "API Key", Widget: apiEntry},
			{Text: "Model", Widget: modelSelect},
		},
		OnSubmit: func() {
			if selectedPreset == nil || apiEntry.Text == "" || modelSelect.Selected == "" {
				dialog.ShowInformation("Missing Fields", "Please fill in all fields.", a.window)
				return
			}
			epID := selectedPreset.DefaultEndpoint
			if len(selectedPreset.Endpoints) > 0 {
				epID = selectedPreset.Endpoints[0].ID
			}
			a.cfg.Vendor = selectedPreset.ID
			a.cfg.Endpoint = epID
			a.cfg.Model = modelSelect.Selected
			a.cfg.SetEndpointAPIKey(selectedPreset.ID, epID, apiEntry.Text, true)
			if err := a.cfg.Save(); err != nil {
				a.showError(fmt.Sprintf("Failed to save config: %v", err))
				return
			}
			a.startChat()
		},
		OnCancel:   func() { a.showWelcome() },
		SubmitText: "Start",
		CancelText: "Back",
	}

	card := widget.NewCard("Setup ggcode", "Configure your AI provider", form)
	a.content.Objects = []fyne.CanvasObject{container.NewCenter(card)}
	a.content.Refresh()
	a.ui.SetStatus("Setup required")
}

// ── Chat ─────────────────────────────────────────────

func (a *App) resumeSession(id string) {
	defer safeRecover("resumeSession")

	// Cancel current work if busy.
	if a.agentBridge != nil {
		a.agentBridge.Cancel()
	}

	// Load session and restore messages into agent.
	if a.agentBridge != nil {
		if err := a.agentBridge.ResumeSession(id); err != nil {
			a.showError(fmt.Sprintf("Failed to resume session: %v", err))
			return
		}
	}

	// Rebuild chat view from session messages.
	if a.chatViewRef != nil && a.agentBridge != nil && a.agentBridge.CurrentSession() != nil {
		a.chatViewRef.rebuildFromMessages(a.agentBridge.CurrentSession().Messages)
	}

	// Push updated session info + history to mobile client
	if a.tunnelBroker != nil && a.agentBridge != nil && a.agentBridge.CurrentSession() != nil {
		a.tunnelBroker.SendSessionInfo(tunnel.SessionInfoData{
			Workspace: a.dc.WorkDir,
			Version:   Version,
		})
		a.tunnelBroker.PushChatClear()
		session := a.agentBridge.CurrentSession()
		history := make([]tunnel.HistoryEntry, 0, len(session.Messages)*2)
		for _, msg := range session.Messages {
			if msg.Role == "user" || msg.Role == "tool" {
				var textParts []string
				for _, block := range msg.Content {
					switch block.Type {
					case "text":
						if strings.TrimSpace(block.Text) != "" {
							textParts = append(textParts, strings.TrimSpace(block.Text))
						}
					case "tool_result":
						result := block.Output
						if len(result) > 500 {
							result = result[:500] + "..."
						}
						history = append(history, tunnel.HistoryEntry{
							Role:     "tool_result",
							ToolID:   block.ToolID,
							ToolName: block.ToolName,
							Result:   result,
							IsError:  block.IsError,
						})
					}
				}
				if len(textParts) > 0 {
					history = append(history, tunnel.HistoryEntry{
						Role:    "user",
						Content: strings.Join(textParts, "\n"),
					})
				}
			} else if msg.Role == "assistant" {
				for _, block := range msg.Content {
					switch block.Type {
					case "text":
						if strings.TrimSpace(block.Text) != "" {
							history = append(history, tunnel.HistoryEntry{
								Role:    "assistant",
								Content: strings.TrimSpace(block.Text),
							})
						}
					case "tool_use":
						detail := ""
						if block.Input != nil {
							var input map[string]interface{}
							if json.Unmarshal(block.Input, &input) == nil {
								if desc, ok := input["description"].(string); ok && desc != "" {
									detail = desc
								} else {
									for _, v := range input {
										if s, ok := v.(string); ok && s != "" {
											detail = s
											break
										}
									}
								}
							}
						}
						history = append(history, tunnel.HistoryEntry{
							Role:     "tool_call",
							ToolID:   block.ToolID,
							ToolName: block.ToolName,
							ToolArgs: detail,
						})
					}
				}
			}
		}
		if len(history) > 0 {
			a.tunnelBroker.PushChatHistory(history)
		}
		a.tunnelBroker.PushStatus(tunnel.StatusIdle, "Ready")
	}

	// Refresh sidebar.
	if a.sidebarRef != nil {
		a.sidebarRef.loadSessions()
		a.sidebarRef.sessionList.Refresh()
	}
}

func (a *App) newSession() {
	defer safeRecover("newSession")

	// 1. Detach broker from bridge FIRST so Cancel() won't enqueue messages.
	if a.agentBridge != nil {
		a.agentBridge.tunnelBroker = nil
	}

	// 2. Cancel current work (won't push to broker now).
	if a.agentBridge != nil {
		a.agentBridge.Cancel()
	}

	// 3. Save current session.
	if a.agentBridge != nil {
		a.agentBridge.saveSession()
	}

	// 4. Send sharing_stopped synchronously, THEN close the connection.
	if a.tunnelSession != nil {
		_ = a.tunnelSession.Send(tunnel.GatewayMessage{Type: "sharing_stopped"})
		a.tunnelSession.Stop()
		a.tunnelSession = nil
		a.tunnelBroker = nil
	}

	// Clear session ID so startChat creates a fresh one.
	if a.agentBridge != nil {
		a.agentBridge.currentSes = nil
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
		a.showError(fmt.Sprintf("Failed to resolve endpoint: %v", err))
		return
	}

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		a.showError(fmt.Sprintf("Failed to create provider: %v", err))
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
	a.ui.SetStatus(fmt.Sprintf("%s/%s | context %s",
		resolved.VendorID, resolved.Model, humanizeTokens(resolved.ContextWindow)))
	a.window.SetTitle(fmt.Sprintf("ggcode — %s [%s]", filepath.Base(a.dc.WorkDir), resolved.Model))

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

// ── Helpers ──────────────────────────────────────────

func (a *App) setTitle(title string) {
	a.window.SetTitle(title)
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
