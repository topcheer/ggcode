package main

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// Sidebar renders the right panel with tabs: Context, Provider, IM.
type Sidebar struct {
	app    *App
	bridge *AgentBridge
	ui     *UIState
	tabs   *container.AppTabs

	// Context tab widgets.
	ctxModelSelect  *widget.Select
	ctxModelLoading *widget.Label
	modeSelect      *widget.Select
	sessionList     *widget.List
	sessions        []sessionMeta
	ctxModelIniting bool // suppress OnChanged during initial SetSelected

	fileTree *FileTree

	// Provider tab widgets.
	vendorSelect   *widget.Select
	epSelect       *widget.Select
	apiKeyEntry    *widget.Entry
	baseURLEntry   *widget.Entry
	modelSelect    *widget.Select
	modelLoading   *widget.Label
	modelRefresh   *widget.Button
	providerStatus *widget.Label
	fetchingModels bool // prevent concurrent fetchModels calls
}

type sessionMeta struct {
	ID   string
	Name string
	Time time.Time
}

func NewSidebar(app *App, bridge *AgentBridge, ui *UIState) *Sidebar {
	return &Sidebar{app: app, bridge: bridge, ui: ui}
}

func (s *Sidebar) buildSessionMetricsCard() fyne.CanvasObject {
	left := widget.NewLabel(strings.Join([]string{
		t("sidebar.metric_turns"),
		t("sidebar.metric_avg_ttft"),
		t("sidebar.metric_p95_ttft"),
		t("sidebar.metric_avg_duration"),
		t("sidebar.metric_p95_duration"),
		t("sidebar.metric_avg_think"),
		t("sidebar.metric_tools"),
		t("sidebar.metric_fail_rate"),
		t("sidebar.metric_slow_tools"),
	}, "\n"))
	right := widget.NewLabelWithData(s.ui.SessionMetricsValueLines)
	right.Alignment = fyne.TextAlignTrailing
	right.TextStyle = fyne.TextStyle{Monospace: true}
	return widget.NewCard(
		t("sidebar.session_metrics_card"),
		"",
		compactPad(0, 0, 0, 0, container.NewHBox(left, layout.NewSpacer(), right)),
	)
}

func (s *Sidebar) buildSessionMetricTurnsCard() fyne.CanvasObject {
	content := widget.NewLabelWithData(s.ui.SessionMetricTurnsLines)
	content.Wrapping = fyne.TextWrapWord
	content.TextStyle = fyne.TextStyle{Monospace: true}
	return widget.NewCard(
		t("sidebar.session_metric_turns_card"),
		"",
		compactPad(0, 0, 0, 0, content),
	)
}

func permissionModeOptions() []string {
	return []string{
		t("sidebar.mode.supervised"),
		t("sidebar.mode.plan"),
		t("sidebar.mode.auto"),
		t("sidebar.mode.bypass"),
		t("sidebar.mode.autopilot"),
	}
}

func permissionModeLabel(mode string) string {
	switch mode {
	case "supervised":
		return t("sidebar.mode.supervised")
	case "plan":
		return t("sidebar.mode.plan")
	case "bypass":
		return t("sidebar.mode.bypass")
	case "autopilot":
		return t("sidebar.mode.autopilot")
	default:
		return t("sidebar.mode.auto")
	}
}

func permissionModeFromLabel(label string) permission.PermissionMode {
	switch label {
	case t("sidebar.mode.supervised"):
		return permission.SupervisedMode
	case t("sidebar.mode.plan"):
		return permission.PlanMode
	case t("sidebar.mode.bypass"):
		return permission.BypassMode
	case t("sidebar.mode.autopilot"):
		return permission.AutopilotMode
	default:
		return permission.AutoMode
	}
}

func (s *Sidebar) Render() fyne.CanvasObject {
	// Build file tree
	s.fileTree = NewFileTree(s.app.dc.WorkDir, func(absPath string) {
		s.app.showFilePreview(absPath, 0)
	})

	// Build file browser tab content: root label + search + tree
	rootName := filepath.Base(s.app.dc.WorkDir)
	if rootName == "" || rootName == "." {
		rootName = t("sidebar.files_root")
	}
	rootLabel := widget.NewLabelWithStyle(rootName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder(t("sidebar.search_files_placeholder"))
	searchEntry.OnChanged = func(text string) {
		s.fileTree.SetFilter(text)
	}
	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		searchEntry.SetText("")
		s.fileTree.Refresh()
	})
	refreshBtn.Importance = widget.LowImportance
	searchRow := container.NewBorder(nil, nil, nil, refreshBtn, searchEntry)
	filesContent := container.NewBorder(
		container.NewVBox(rootLabel, searchRow),
		nil, nil, nil,
		s.fileTree.Widget(),
	)

	s.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon(t("sidebar.tab.context"), theme.InfoIcon(), s.buildContextTab()),
		container.NewTabItemWithIcon(t("sidebar.tab.provider"), theme.ComputerIcon(), s.buildProviderTab()),
		container.NewTabItemWithIcon(t("sidebar.tab.files"), theme.FolderIcon(), filesContent),
	)
	s.tabs.OnSelected = func(tab *container.TabItem) {
		if tab.Text == t("sidebar.tab.provider") {
			s.fetchModels()
		}
	}
	hideBtn := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		s.app.toggleSidebar()
	})
	hideBtn.Importance = widget.LowImportance

	topBar := container.NewHBox(layout.NewSpacer(), hideBtn)
	return container.NewBorder(compactPad(2, 0, 0, 0, topBar), nil, nil, nil, s.tabs)
}

func (s *Sidebar) RefreshStats() {
	resolved := s.bridge.Resolved()
	if s.ctxModelSelect != nil && resolved.Model != "" && s.ctxModelSelect.Selected != resolved.Model {
		s.ctxModelSelect.SetSelected(resolved.Model)
	}
}

// ── Context tab ──────────────────────────────────────

func (s *Sidebar) buildContextTab() fyne.CanvasObject {
	resolved := s.bridge.Resolved()

	// Init loading label before the select callback can fire via SetSelected.
	s.ctxModelLoading = widget.NewLabel("")

	s.ctxModelIniting = true
	s.ctxModelSelect = widget.NewSelect(nil, func(model string) {
		if model == "" || s.bridge == nil || s.ctxModelIniting {
			return
		}
		if err := s.bridge.SwitchModel(model); err != nil {
			logf("sidebar", "switch model failed: %v", err)
			return
		}
		s.ctxModelLoading.SetText("")
		s.RefreshStats()
	})
	s.ctxModelSelect.PlaceHolder = t("sidebar.select_model_placeholder")

	// Populate model list from resolved endpoint config.
	if len(resolved.Models) > 0 {
		s.ctxModelSelect.Options = resolved.Models
	}
	if resolved.Model != "" {
		s.ctxModelSelect.SetSelected(resolved.Model)
	}
	s.ctxModelIniting = false

	// Permission mode selector.
	modeOptions := permissionModeOptions()
	s.modeSelect = widget.NewSelect(modeOptions, func(sel string) {
		if s.bridge == nil {
			return
		}
		m := permissionModeFromLabel(sel)
		s.bridge.SetPermissionMode(m)

		// Persist to workspace config.
		if s.app.cfg != nil {
			_ = s.app.cfg.SaveDefaultModePreference(m.String())
		}
	})

	// Read initial mode from config, default to "auto".
	initialMode := "auto"
	if s.app.cfg != nil && s.app.cfg.DefaultMode != "" {
		initialMode = s.app.cfg.DefaultMode
	}
	switch initialMode {
	default:
		s.modeSelect.SetSelected(permissionModeLabel(initialMode))
	}

	refreshModelsBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		s.fetchContextModels()
	})
	refreshModelsBtn.Importance = widget.LowImportance

	infoCard := widget.NewCard(t("sidebar.model_info_card"), "", widget.NewForm(
		&widget.FormItem{Text: t("sidebar.vendor_label"), Widget: widget.NewLabel(resolved.VendorName)},
		&widget.FormItem{Text: t("sidebar.model_label"), Widget: container.NewBorder(nil, nil, nil, refreshModelsBtn, s.ctxModelSelect)},
		&widget.FormItem{Text: t("sidebar.mode_label"), Widget: s.modeSelect},
	))

	// Session list.
	s.loadSessions()
	s.sessionList = widget.NewList(
		func() int { return len(s.sessions) },
		func() fyne.CanvasObject {
			nameLabel := widget.NewLabel(t("sidebar.session"))
			nameLabel.Wrapping = fyne.TextWrapWord
			timeLabel := widget.NewLabel(t("sidebar.time"))
			timeLabel.TextStyle = fyne.TextStyle{Monospace: true}
			return container.NewBorder(nil, nil, nil, timeLabel,
				container.NewPadded(nameLabel),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(s.sessions) {
				return
			}
			box := obj.(*fyne.Container)
			padded := box.Objects[0].(*fyne.Container)
			nameLabel := padded.Objects[0].(*widget.Label)
			timeLabel := box.Objects[1].(*widget.Label)
			sess := s.sessions[id]
			nameLabel.SetText(sess.Name)
			timeLabel.SetText(sess.Time.Format("01-02 15:04"))
		},
	)

	s.sessionList.OnSelected = func(id widget.ListItemID) {
		if id >= len(s.sessions) {
			return
		}
		sess := s.sessions[id]
		s.app.resumeSession(sess.ID)
	}

	sessionHeader := widget.NewLabel(t("sidebar.sessions"))
	sessionHeader.TextStyle = fyne.TextStyle{Bold: true}

	newChatBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		s.app.newSession()
	})
	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		s.showImpersonateDialog()
	})
	settingsBtn.Importance = widget.LowImportance

	shareBtn := widget.NewButtonWithIcon("", theme.ViewFullScreenIcon(), func() {
		s.app.showShareDialog()
	})
	shareBtn.Importance = widget.LowImportance

	bottomBar := container.NewHBox(newChatBtn, layout.NewSpacer(), shareBtn, settingsBtn)
	topSection := container.NewVBox(infoCard, container.NewPadded(sessionHeader))
	return container.NewBorder(topSection, bottomBar, nil, nil, s.sessionList)
}

func (s *Sidebar) loadSessions() {
	workspace := s.app.dc.WorkDir
	logf("sidebar", "loadSessions: workspace=%s", workspace)

	store, err := session.NewDefaultStore()
	if err != nil {
		logf("sidebar", "loadSessions: store error: %v", err)
		s.sessions = nil
		return
	}
	allSessions, err := store.List()
	if err != nil {
		logf("sidebar", "loadSessions: list error: %v", err)
		s.sessions = nil
		return
	}

	logf("sidebar", "loadSessions: total=%d", len(allSessions))

	// Normalize workspace for comparison (resolve symlinks, clean path).
	normalizedWS := session.NormalizeWorkspacePath(workspace)

	var filtered []*session.Session
	for _, sess := range allSessions {
		if sess.Workspace == workspace || sess.Workspace == normalizedWS {
			filtered = append(filtered, sess)
		}
	}
	logf("sidebar", "loadSessions: workspace=%s filtered=%d", workspace, len(filtered))

	// No fallback to all sessions — only show matching workspace sessions.

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})

	if len(filtered) > 5 {
		filtered = filtered[:5]
	}

	s.sessions = make([]sessionMeta, 0, len(filtered))
	for _, sess := range filtered {
		name := sess.Title
		if name == "" {
			name = sess.ID
			if len([]rune(name)) > 8 {
				name = string([]rune(name)[:8])
			}
		}
		if len([]rune(name)) > 50 {
			name = string([]rune(name)[:50]) + "..."
		}
		s.sessions = append(s.sessions, sessionMeta{
			ID:   sess.ID,
			Name: name,
			Time: sess.UpdatedAt,
		})
	}
}

// fetchContextModels discovers models from the active provider and updates the
// context tab model selector.
func (s *Sidebar) fetchContextModels() {
	s.ctxModelLoading.SetText(t("status.loading"))
	s.ctxModelLoading.Refresh()

	resolved := s.bridge.Resolved()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		models, err := provider.DiscoverModels(ctx, resolved)
		fyne.Do(func() {
			if err != nil {
				s.ctxModelLoading.SetText(t("status.failed"))
				return
			}
			if len(models) > 0 {
				// Preserve current selection.
				current := s.ctxModelSelect.Selected
				s.ctxModelSelect.Options = models
				s.ctxModelSelect.Refresh()
				if current != "" {
					for _, m := range models {
						if m == current {
							s.ctxModelSelect.SetSelected(current)
							break
						}
					}
				}
				// Update config model list for future use.
				if s.app.cfg != nil {
					_ = s.app.cfg.SetEndpointModels(resolved.VendorID, resolved.EndpointID, models)
				}
			}
			s.ctxModelLoading.SetText(t("sidebar.models_count", len(models)))
		})
	}()
}

// ── Provider tab ─────────────────────────────────────

func (s *Sidebar) buildProviderTab() fyne.CanvasObject {
	cfg := s.app.cfg
	resolved := s.bridge.Resolved()

	// Create ALL widgets first (before setting any values that trigger callbacks).
	vendorNames := make([]string, 0, len(cfg.Vendors))
	for name := range cfg.Vendors {
		vendorNames = append(vendorNames, name)
	}
	sort.Strings(vendorNames)

	s.vendorSelect = widget.NewSelect(vendorNames, func(vendor string) {
		if s.ui.AgentWorking.Load() {
			return
		}
		s.updateEndpoints(vendor)
		s.fetchModels()
	})
	s.epSelect = widget.NewSelect([]string{}, func(ep string) {
		if s.ui.AgentWorking.Load() {
			return
		}
		s.onEndpointChange(s.vendorSelect.Selected, ep)
		s.fetchModels()
	})
	s.apiKeyEntry = widget.NewPasswordEntry()
	s.apiKeyEntry.PlaceHolder = t("sidebar.api_key_label")
	s.baseURLEntry = widget.NewEntry()
	s.baseURLEntry.PlaceHolder = "https://api.example.com/v1"
	s.modelLoading = widget.NewLabel("")
	s.modelSelect = widget.NewSelect([]string{}, nil)
	s.modelSelect.PlaceHolder = t("sidebar.select_model_placeholder")
	s.modelRefresh = widget.NewButtonWithIcon(t("sidebar.refresh_models"), theme.ViewRefreshIcon(), func() {
		if s.ui.AgentWorking.Load() {
			return
		}
		s.fetchModels()
	})
	s.providerStatus = widget.NewLabel("")

	// Now set values (callbacks safe - all widgets created above).
	s.vendorSelect.SetSelected(cfg.Vendor)
	s.updateEndpoints(cfg.Vendor)
	s.epSelect.SetSelected(cfg.Endpoint)
	s.apiKeyEntry.SetText(resolved.APIKey)
	s.baseURLEntry.SetText(resolved.BaseURL)
	if resolved.Model != "" {
		s.modelSelect.SetSelected(resolved.Model)
	}

	// Apply button.
	applyBtn := widget.NewButtonWithIcon(t("sidebar.apply_restart"), theme.ConfirmIcon(), func() {
		if s.ui.AgentWorking.Load() {
			return
		}
		s.applyProvider()
	})
	applyBtn.Importance = widget.HighImportance

	return container.NewVScroll(container.NewVBox(
		widget.NewCard(t("sidebar.provider_card"), "", container.NewVBox(
			widget.NewForm(
				&widget.FormItem{Text: t("sidebar.vendor_label"), Widget: s.vendorSelect},
				&widget.FormItem{Text: t("sidebar.endpoint_label"), Widget: s.epSelect},
				&widget.FormItem{Text: t("sidebar.api_key_label"), Widget: s.apiKeyEntry},
				&widget.FormItem{Text: t("sidebar.base_url_label"), Widget: s.baseURLEntry},
			),
		)),
		widget.NewCard(t("sidebar.model_card"), "", container.NewVBox(
			s.modelSelect,
			container.NewHBox(s.modelRefresh, s.modelLoading),
		)),
		s.providerStatus,
		applyBtn,
		widget.NewButtonWithIcon(t("sidebar.add_endpoint"), theme.ContentAddIcon(), func() {
			s.showAddEndpointDialog()
		}),
	))
}

func (s *Sidebar) updateEndpoints(vendor string) {
	cfg := s.app.cfg
	if v, ok := cfg.Vendors[vendor]; ok {
		eps := make([]string, 0, len(v.Endpoints))
		for name := range v.Endpoints {
			eps = append(eps, name)
		}
		sort.Strings(eps)
		s.epSelect.Options = eps
		s.epSelect.Refresh()
		if len(eps) > 0 {
			s.epSelect.SetSelected(eps[0])
		}

		// Update API key from vendor.
		if v.APIKey != "" {
			s.apiKeyEntry.SetText(v.APIKey)
		}
	}
}

func (s *Sidebar) onEndpointChange(vendor, endpoint string) {
	cfg := s.app.cfg
	if v, ok := cfg.Vendors[vendor]; ok {
		if ep, ok := v.Endpoints[endpoint]; ok {
			if ep.BaseURL != "" {
				s.baseURLEntry.SetText(ep.BaseURL)
			}
			if ep.APIKey != "" {
				s.apiKeyEntry.SetText(ep.APIKey)
			} else if v.APIKey != "" {
				s.apiKeyEntry.SetText(v.APIKey)
			}
			// Set model from config if available.
			model := ep.SelectedModel
			if model == "" {
				model = ep.DefaultModel
			}
			if model != "" {
				s.modelSelect.SetSelected(model)
			} else {
				s.modelSelect.ClearSelected()
			}
		}
	}
}

func (s *Sidebar) fetchModels() {
	if s.fetchingModels {
		return
	}
	s.fetchingModels = true
	defer func() { s.fetchingModels = false }()

	s.modelLoading.SetText(t("sidebar.model_loading"))
	s.modelLoading.Refresh()

	resolved := s.bridge.Resolved()
	// Build a temporary resolved with current form values.
	tmpResolved := &config.ResolvedEndpoint{
		VendorID:   s.vendorSelect.Selected,
		EndpointID: s.epSelect.Selected,
		Protocol:   resolved.Protocol,
		BaseURL:    s.baseURLEntry.Text,
		APIKey:     s.apiKeyEntry.Text,
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		models, err := provider.DiscoverModels(ctx, tmpResolved)
		fyne.Do(func() {
			if err != nil {
				s.modelSelect.Options = []string{}
				s.modelSelect.Refresh()
				s.modelLoading.SetText(t("sidebar.model_refresh_failed"))
				return
			}
			s.modelSelect.Options = models
			if s.modelSelect.Options == nil {
				s.modelSelect.Options = []string{}
			}
			s.modelSelect.Refresh()
			if len(models) > 0 {
				s.modelSelect.SetSelected(models[0])
			}
			s.modelLoading.SetText(t("sidebar.models_found_ok", len(models)))
		})
	}()
}

func (s *Sidebar) applyProvider() {
	cfg := s.app.cfg
	vendor := s.vendorSelect.Selected
	endpoint := s.epSelect.Selected
	apiKey := s.apiKeyEntry.Text
	baseURL := s.baseURLEntry.Text
	model := s.modelSelect.Selected

	// Update config.
	cfg.Vendor = vendor
	cfg.Endpoint = endpoint
	cfg.Model = model

	// Save API key securely.
	if apiKey != "" {
		_ = cfg.SetEndpointAPIKey(vendor, endpoint, apiKey, false)
	}

	// Update base URL in endpoint config.
	if v, ok := cfg.Vendors[vendor]; ok {
		if ep, ok := v.Endpoints[endpoint]; ok {
			ep.BaseURL = baseURL
			v.Endpoints[endpoint] = ep
			cfg.Vendors[vendor] = v
		}
	}

	// Save selected model.
	if v, ok := cfg.Vendors[vendor]; ok {
		if ep, ok := v.Endpoints[endpoint]; ok {
			ep.SelectedModel = model
			v.Endpoints[endpoint] = ep
			cfg.Vendors[vendor] = v
		}
	}

	_ = cfg.Save()
	s.providerStatus.SetText(t("sidebar.saved_restarting"))
	s.providerStatus.Refresh()

	// Restart chat with new settings.
	s.app.startChat()
}

// ── IM tab ───────────────────────────────────────────

func (s *Sidebar) showImpersonateDialog() {
	presets := provider.DefaultImpersonationPresets()
	names := make([]string, len(presets))
	for i, p := range presets {
		names[i] = p.DisplayName
	}

	// Find current selection.
	currentPreset := "none"
	if s.app.cfg != nil && s.app.cfg.Impersonation.Preset != "" {
		currentPreset = s.app.cfg.Impersonation.Preset
	}

	selectEntry := widget.NewSelect(names, nil)
	for i, p := range presets {
		if p.ID == currentPreset {
			selectEntry.SetSelectedIndex(i)
			break
		}
	}

	versionEntry := widget.NewEntry()
	versionEntry.SetPlaceHolder(t("sidebar.custom_version_placeholder"))
	if s.app.cfg != nil && s.app.cfg.Impersonation.CustomVersion != "" {
		versionEntry.SetText(s.app.cfg.Impersonation.CustomVersion)
	}

	form := &widget.Form{
		Items: []*widget.FormItem{
			{Text: t("sidebar.identity_label"), Widget: selectEntry},
			{Text: t("sidebar.version_label"), Widget: versionEntry},
		},
		OnSubmit: func() {
			idx := selectEntry.SelectedIndex()
			if idx < 0 || idx >= len(presets) {
				return
			}
			selected := presets[idx]
			version := strings.TrimSpace(versionEntry.Text)

			// Apply globally.
			var presetPtr *provider.ImpersonationPreset
			if selected.ID != "none" {
				presetPtr = &selected
			}
			provider.SetActiveImpersonation(presetPtr, version, nil)

			// Persist to config.
			if s.app.cfg != nil {
				s.app.cfg.Impersonation = config.ImpersonationConfig{
					Preset:        selected.ID,
					CustomVersion: version,
				}
				_ = s.app.cfg.Save()
			}

			// Reset agent so next request uses new headers.
			s.app.agentBridge.ResetAgent()
		},
	}

	d := dialog.NewCustomConfirm(t("sidebar.impersonation_title"), t("common.apply"), t("common.cancel"), form, func(ok bool) {
		if ok {
			form.OnSubmit()
		}
	}, s.app.window)
	d.Resize(fyne.NewSize(400, 250))
	d.Show()
}

// showAddEndpointDialog shows a form to add a new endpoint to the current vendor.
func (s *Sidebar) showAddEndpointDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder(t("sidebar.endpoint_name_placeholder"))

	protocolSelect := widget.NewSelect([]string{"openai", "anthropic", "google"}, nil)
	protocolSelect.SetSelected("openai")

	apiKeyEntry := widget.NewPasswordEntry()
	apiKeyEntry.SetPlaceHolder("sk-...")

	baseURLEntry := widget.NewEntry()
	baseURLEntry.SetPlaceHolder("https://api.example.com/v1")

	statusLabel := widget.NewLabel("")

	testBtn := widget.NewButton(t("sidebar.test_connection"), func() {
		baseURL := strings.TrimSpace(baseURLEntry.Text)
		if baseURL == "" {
			statusLabel.SetText(t("sidebar.base_url_required"))
			return
		}
		statusLabel.SetText(t("sidebar.testing"))
		go func() {
			tmpResolved := &config.ResolvedEndpoint{
				Protocol: protocolSelect.Selected,
				BaseURL:  baseURL,
			}
			if apiKey := strings.TrimSpace(apiKeyEntry.Text); apiKey != "" {
				tmpResolved.APIKey = apiKey
			}
			models, err := provider.DiscoverModels(context.Background(), tmpResolved)
			fyne.Do(func() {
				if err != nil {
					statusLabel.SetText(t("status.failed"))
				} else {
					statusLabel.SetText(t("sidebar.models_found_ok", len(models)))
				}
			})
		}()
	})

	form := container.NewVBox(
		widget.NewForm(
			&widget.FormItem{Text: t("sidebar.form.name"), Widget: nameEntry},
			&widget.FormItem{Text: t("sidebar.form.protocol"), Widget: protocolSelect},
			&widget.FormItem{Text: t("sidebar.api_key_label"), Widget: apiKeyEntry},
			&widget.FormItem{Text: t("sidebar.base_url_label"), Widget: baseURLEntry},
		),
		container.NewHBox(testBtn, statusLabel),
	)

	d := dialog.NewCustomConfirm(t("sidebar.endpoint_add_title"), t("common.add"), t("common.cancel"), form, func(ok bool) {
		if !ok {
			return
		}
		name := strings.TrimSpace(nameEntry.Text)
		if name == "" {
			return
		}
		vendor := s.vendorSelect.Selected
		if vendor == "" {
			return
		}
		// Add endpoint to config.
		ep := config.EndpointConfig{
			DisplayName: name,
			Protocol:    protocolSelect.Selected,
			BaseURL:     strings.TrimSpace(baseURLEntry.Text),
		}
		if apiKey := strings.TrimSpace(apiKeyEntry.Text); apiKey != "" {
			ep.APIKey = apiKey
		}
		if s.app.cfg.Vendors[vendor].Endpoints == nil {
			vc := s.app.cfg.Vendors[vendor]
			vc.Endpoints = map[string]config.EndpointConfig{}
			s.app.cfg.Vendors[vendor] = vc
		}
		vc := s.app.cfg.Vendors[vendor]
		vc.Endpoints[name] = ep
		s.app.cfg.Vendors[vendor] = vc
		_ = s.app.cfg.Save()

		// Refresh UI.
		s.updateEndpoints(vendor)
		s.epSelect.SetSelected(name)
	}, s.app.window)
	d.Resize(fyne.NewSize(500, 350))
	d.Show()
}
