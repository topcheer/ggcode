package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// App is the top-level desktop application state.
type App struct {
	fyneApp fyne.App
	window  fyne.Window
	dc      *DesktopConfig
	cfg     *config.Config

	// Shared UI state for cross-goroutine updates.
	ui *UIState

	// UI components.
	content   *fyne.Container
	statusBar *widget.Label

	// Agent state.
	agentBridge *AgentBridge
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
		fyne.NewMenuItem("Refresh Stats", func() { a.refreshSidebar() }),
	)
	a.window.SetMainMenu(fyne.NewMainMenu(fileMenu, viewMenu))
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
	dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
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
}

// ── Init from workDir ────────────────────────────────

func (a *App) initFromWorkDir(dir string) {
	defer safeRecover("initFromWorkDir")

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

func (a *App) startChat() {
	defer safeRecover("startChat")

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

	chatView := NewChatView(bridge, a.ui)
	sidebar := NewSidebar(a, bridge, a.ui)

	split := container.NewHSplit(chatView.Render(), sidebar.Render())
	split.SetOffset(0.7)

	a.content.Objects = []fyne.CanvasObject{split}
	a.content.Refresh()

	a.ui.SetModelInfo(resolved.Model, humanizeTokens(resolved.ContextWindow))
	a.ui.SetStatus(fmt.Sprintf("%s/%s | context %s",
		resolved.VendorID, resolved.Model, humanizeTokens(resolved.ContextWindow)))
	a.window.SetTitle(fmt.Sprintf("ggcode — %s [%s]", filepath.Base(a.dc.WorkDir), resolved.Model))

	// Background poll for stats (writes only to bindings — safe).
	go a.pollStats(bridge)
}

func (a *App) pollStats(bridge *AgentBridge) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if bridge == nil {
			continue
		}
		tc := bridge.TokenCount()
		cw := bridge.ContextWindow()
		resolved := bridge.Resolved()
		working := ""
		if bridge.IsWorking() {
			working = " | working..."
		}
		a.ui.SetTokenUsage(fmt.Sprintf("%s / %s", humanizeTokens(tc), humanizeTokens(cw)), float64(tc)/float64(max(cw, 1)))
		a.ui.SetStatus(fmt.Sprintf("%s/%s | %s/%s%s",
			resolved.VendorID, resolved.Model,
			humanizeTokens(tc), humanizeTokens(cw),
			working))
	}
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
