package main

import (
	"fmt"
	"image/color"
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
	cfg     *config.Config // ggcode config (nil before init)

	// UI components.
	content   *fyne.Container
	statusBar *widget.Label
	sidebar   *Sidebar
	chatView  *ChatView

	// Agent state.
	agentBridge *AgentBridge
}

// NewApp creates the desktop app.
func NewApp(fyneApp fyne.App) *App {
	return &App{
		fyneApp: fyneApp,
		dc:      LoadDesktopConfig(),
	}
}

// Run shows the window and starts the event loop.
func (a *App) Run() {
	a.window = a.fyneApp.NewWindow("ggcode")
	a.buildUI()
	a.setupMenu()

	w := float32(1200)
	h := float32(800)
	if a.dc.WindowW > 0 {
		w = float32(a.dc.WindowW)
	}
	if a.dc.WindowH > 0 {
		h = float32(a.dc.WindowH)
	}
	a.window.Resize(fyne.NewSize(w, h))

	// Decide initial view.
	if a.dc.WorkDir == "" {
		a.showWelcome()
	} else {
		a.initFromWorkDir(a.dc.WorkDir)
	}

	a.window.SetOnClosed(func() {
		// Save window size.
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
	a.statusBar = widget.NewLabel("Ready")
	a.statusBar.TextStyle = fyne.TextStyle{Monospace: true}

	statusBox := container.NewHBox(
		widget.NewIcon(theme.ComputerIcon()),
		a.statusBar,
		layout.NewSpacer(),
		widget.NewIcon(theme.InfoIcon()),
	)

	a.content = container.NewStack(widget.NewLabel(""))

	root := container.NewBorder(
		nil,       // top
		statusBox, // bottom
		nil,       // left
		nil,       // right
		a.content,
	)
	a.window.SetContent(root)
}

func (a *App) setupMenu() {
	fileMenu := fyne.NewMenu("File",
		fyne.NewMenuItem("Open Project...", func() {
			a.showFolderPicker()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() {
			a.fyneApp.Quit()
		}),
	)

	viewMenu := fyne.NewMenu("View",
		fyne.NewMenuItem("Refresh Stats", func() {
			if a.sidebar != nil {
				a.sidebar.RefreshStats()
			}
		}),
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
	a.statusBar.SetText("Select a project directory")
	a.window.SetTitle("ggcode — Welcome")
}

func (a *App) showFolderPicker() {
	dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil {
			return // cancelled
		}
		path := u.Path()
		if path == "" {
			// Fallback: try parsing the URI string.
			path = filepath.Join(u.String())
		}
		if path != "" {
			a.initFromWorkDir(path)
		}
	}, a.window)
}

// ── Init from workDir ────────────────────────────────

func (a *App) initFromWorkDir(dir string) {
	a.statusBar.SetText(fmt.Sprintf("Loading %s...", dir))
	a.window.SetTitle(fmt.Sprintf("ggcode — %s", filepath.Base(dir)))

	cfgPath := resolveConfigFilePath(dir)
	cfg, err := config.LoadWithInstance(cfgPath, dir)
	if err != nil {
		a.showError(fmt.Sprintf("Failed to load config: %v", err))
		return
	}
	a.cfg = cfg

	// Persist work dir.
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
	errIcon := widget.NewIcon(theme.ErrorIcon())

	retryBtn := widget.NewButton("Choose Another Directory", func() {
		a.showFolderPicker()
	})

	card := widget.NewCard("Error", "", container.NewVBox(
		container.NewHBox(errIcon, errLabel),
		retryBtn,
	))

	a.content.Objects = []fyne.CanvasObject{card}
	a.content.Refresh()
	a.statusBar.SetText("Error")
}

// ── Onboard wizard ────────────────────────────────────

func (a *App) showOnboard() {
	presets := config.VendorPresets()
	if len(presets) == 0 {
		a.showError("No vendor presets available. Please configure manually in ggcode.yaml.")
		return
	}

	// Build vendor cards.
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

	// When vendor changes, update available models.
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
				modelSelect.Options = []string{"(error: " + err.Error() + ")"}
				modelSelect.Refresh()
				return
			}
			models := resolved.Models
			if len(models) == 0 {
				models = []string{resolved.Model}
			}
			modelSelect.Options = models
		} else {
			// Show endpoint's default models without API key.
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
			a.completeOnboard(selectedPreset.ID, epID, apiEntry.Text, modelSelect.Selected)
		},
		OnCancel: func() {
			a.showWelcome()
		},
		SubmitText: "Start",
		CancelText: "Back",
	}

	card := widget.NewCard("Setup ggcode", "Configure your AI provider", form)

	a.content.Objects = []fyne.CanvasObject{
		container.NewCenter(card),
	}
	a.content.Refresh()
	a.statusBar.SetText("Setup required")
}

func (a *App) completeOnboard(vendorID, endpointID, apiKey, model string) {
	a.cfg.Vendor = vendorID
	a.cfg.Endpoint = endpointID
	a.cfg.Model = model
	a.cfg.SetEndpointAPIKey(vendorID, endpointID, apiKey, true)

	if err := a.cfg.Save(); err != nil {
		a.showError(fmt.Sprintf("Failed to save config: %v", err))
		return
	}

	a.startChat()
}

// ── Chat ─────────────────────────────────────────────

func (a *App) startChat() {
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

	workDir := a.dc.WorkDir
	bridge := NewAgentBridge(a.cfg, prov, resolved, workDir)
	a.agentBridge = bridge

	a.chatView = NewChatView(a, bridge)
	a.sidebar = NewSidebar(a, bridge)

	// HSplit: 70% chat, 30% sidebar.
	split := container.NewHSplit(
		a.chatView.Render(),
		a.sidebar.Render(),
	)
	split.SetOffset(0.7)

	a.content.Objects = []fyne.CanvasObject{split}
	a.content.Refresh()

	// Update status bar.
	a.statusBar.SetText(fmt.Sprintf("%s/%s | context %s",
		resolved.VendorID, resolved.Model, humanizeTokens(resolved.ContextWindow)))
	a.window.SetTitle(fmt.Sprintf("ggcode — %s [%s]", filepath.Base(workDir), resolved.Model))

	// Periodically refresh sidebar stats.
	go a.pollStats()
}

func (a *App) pollStats() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if a.sidebar == nil || a.agentBridge == nil {
			continue
		}
		a.sidebar.RefreshStats()

		// Update status bar with token count.
		tc := a.agentBridge.TokenCount()
		cw := a.agentBridge.ContextWindow()
		resolved := a.agentBridge.Resolved()
		working := ""
		if a.agentBridge.IsWorking() {
			working = " | working..."
		}
		a.statusBar.SetText(fmt.Sprintf("%s/%s | %s/%s%s",
			resolved.VendorID, resolved.Model,
			humanizeTokens(tc), humanizeTokens(cw),
			working))
	}
}

// ── Helpers ──────────────────────────────────────────

func resolveConfigFilePath(workDir string) string {
	// Same resolution as TUI: ./ggcode.yaml → ./.ggcode/ggcode.yaml → global
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

func themeTextColor() color.Color {
	return theme.ForegroundColor()
}
