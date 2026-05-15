package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/config"
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
	contextLabel *widget.Label
	tokenLabel   *widget.Label
	tokenBar     *widget.ProgressBar
	modelLabel   *widget.Label
	sessionList  *widget.List
	sessions     []sessionMeta

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

func (s *Sidebar) Render() fyne.CanvasObject {
	s.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Context", theme.InfoIcon(), s.buildContextTab()),
		container.NewTabItemWithIcon("Provider", theme.ComputerIcon(), s.buildProviderTab()),
		container.NewTabItemWithIcon("IM", theme.MailComposeIcon(), s.buildIMTab()),
	)
	s.tabs.OnSelected = func(tab *container.TabItem) {
		if tab.Text == "Provider" {
			s.fetchModels()
		}
	}
	return s.tabs
}

func (s *Sidebar) RefreshStats() {
	cw := s.bridge.ContextWindow()
	tc := s.bridge.TokenCount()
	resolved := s.bridge.Resolved()

	s.modelLabel.SetText(resolved.Model)
	s.contextLabel.SetText(humanizeTokens(cw))
	s.tokenLabel.SetText(fmt.Sprintf("%s / %s", humanizeTokens(tc), humanizeTokens(cw)))
	if cw > 0 {
		s.tokenBar.SetValue(float64(tc) / float64(cw))
	} else {
		s.tokenBar.SetValue(0)
	}
}

// ── Context tab ──────────────────────────────────────

func (s *Sidebar) buildContextTab() fyne.CanvasObject {
	resolved := s.bridge.Resolved()

	s.modelLabel = widget.NewLabel(resolved.Model)
	s.contextLabel = widget.NewLabel(humanizeTokens(resolved.ContextWindow))
	s.tokenLabel = widget.NewLabel("0 / " + humanizeTokens(resolved.ContextWindow))
	s.tokenBar = widget.NewProgressBar()
	s.tokenBar.Max = 1.0

	infoCard := widget.NewCard("Model Info", "", widget.NewForm(
		&widget.FormItem{Text: "Vendor", Widget: widget.NewLabel(resolved.VendorName)},
		&widget.FormItem{Text: "Model", Widget: s.modelLabel},
		&widget.FormItem{Text: "Context", Widget: s.contextLabel},
	))

	statsCard := widget.NewCard("Usage", "", container.NewVBox(
		s.tokenLabel,
		s.tokenBar,
	))

	// Session list.
	s.loadSessions()
	s.sessionList = widget.NewList(
		func() int { return len(s.sessions) },
		func() fyne.CanvasObject {
			nameLabel := widget.NewLabel("session")
			nameLabel.Wrapping = fyne.TextWrapWord
			timeLabel := widget.NewLabel("time")
			timeLabel.TextStyle = fyne.TextStyle{Monospace: true}
			return container.NewBorder(nil, nil, nil, timeLabel, nameLabel)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(s.sessions) {
				return
			}
			box := obj.(*fyne.Container)
			nameLabel := box.Objects[0].(*widget.Label)
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

	sessionHeader := widget.NewLabel("Sessions")
	sessionHeader.TextStyle = fyne.TextStyle{Bold: true}

	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		s.showImpersonateDialog()
	})
	settingsBtn.Importance = widget.LowImportance

	topSection := container.NewVBox(infoCard, statsCard, container.NewPadded(sessionHeader))
	return container.NewBorder(topSection, container.NewPadded(settingsBtn), nil, nil, s.sessionList)
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

// ── Provider tab ─────────────────────────────────────

func (s *Sidebar) buildProviderTab() fyne.CanvasObject {
	cfg := s.app.cfg
	resolved := s.bridge.Resolved()

	// Create ALL widgets first (before setting any values that trigger callbacks).
	vendorNames := make([]string, 0, len(cfg.Vendors))
	for name := range cfg.Vendors {
		vendorNames = append(vendorNames, name)
	}

	s.vendorSelect = widget.NewSelect(vendorNames, func(vendor string) {
		s.updateEndpoints(vendor)
		s.fetchModels()
	})
	s.epSelect = widget.NewSelect([]string{}, func(ep string) {
		s.onEndpointChange(s.vendorSelect.Selected, ep)
		s.fetchModels()
	})
	s.apiKeyEntry = widget.NewPasswordEntry()
	s.apiKeyEntry.PlaceHolder = "API Key"
	s.baseURLEntry = widget.NewEntry()
	s.baseURLEntry.PlaceHolder = "https://api.example.com/v1"
	s.modelLoading = widget.NewLabel("")
	s.modelSelect = widget.NewSelect([]string{}, nil)
	s.modelSelect.PlaceHolder = "Select model..."
	s.modelRefresh = widget.NewButtonWithIcon("Refresh Models", theme.ViewRefreshIcon(), func() {
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
	applyBtn := widget.NewButtonWithIcon("Apply & Restart", theme.ConfirmIcon(), func() {
		s.applyProvider()
	})
	applyBtn.Importance = widget.HighImportance

	return container.NewVScroll(container.NewVBox(
		widget.NewCard("Provider", "", container.NewVBox(
			widget.NewForm(
				&widget.FormItem{Text: "Vendor", Widget: s.vendorSelect},
				&widget.FormItem{Text: "Endpoint", Widget: s.epSelect},
				&widget.FormItem{Text: "API Key", Widget: s.apiKeyEntry},
				&widget.FormItem{Text: "Base URL", Widget: s.baseURLEntry},
			),
		)),
		widget.NewCard("Model", "", container.NewVBox(
			s.modelSelect,
			container.NewHBox(s.modelRefresh, s.modelLoading),
		)),
		s.providerStatus,
		applyBtn,
	))
}

func (s *Sidebar) updateEndpoints(vendor string) {
	cfg := s.app.cfg
	if v, ok := cfg.Vendors[vendor]; ok {
		eps := make([]string, 0, len(v.Endpoints))
		for name := range v.Endpoints {
			eps = append(eps, name)
		}
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

	s.modelLoading.SetText("Loading...")
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
				s.modelLoading.SetText("Failed to refresh models")
				return
			}
			s.modelSelect.Options = models
			if s.modelSelect.Options == nil {
				s.modelSelect.Options = []string{}
			}
			s.modelSelect.Refresh()
			s.modelLoading.SetText(fmt.Sprintf("%d models found", len(models)))
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
	s.providerStatus.SetText("Saved. Restarting chat...")
	s.providerStatus.Refresh()

	// Restart chat with new settings.
	s.app.startChat()
}

// ── IM tab ───────────────────────────────────────────

func (s *Sidebar) buildIMTab() fyne.CanvasObject {
	cfg := s.app.cfg
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}

	imStatus := widget.NewLabel("")

	// ── Adapter list ──
	adapterNames := sortedAdapterNames(cfg)
	adapterList := widget.NewList(
		func() int { return len(adapterNames) },
		func() fyne.CanvasObject {
			nameLbl := widget.NewLabel("adapter")
			platLbl := widget.NewLabel("platform")
			platLbl.TextStyle = fyne.TextStyle{Italic: true}
			statusIcon := widget.NewIcon(theme.ConfirmIcon())
			return container.NewBorder(nil, nil, statusIcon, platLbl, nameLbl)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(adapterNames) {
				return
			}
			box := obj.(*fyne.Container)
			var nameLbl, platLbl *widget.Label
			var icon *widget.Icon
			for _, o := range box.Objects {
				switch v := o.(type) {
				case *widget.Label:
					if nameLbl == nil {
						nameLbl = v
					} else {
						platLbl = v
					}
				case *widget.Icon:
					icon = v
				}
			}
			name := adapterNames[id]
			adapter := cfg.IM.Adapters[name]
			if nameLbl != nil {
				nameLbl.SetText(name)
			}
			if platLbl != nil {
				platLbl.SetText(platformDisplayName(adapter.Platform))
			}
			if icon != nil {
				if adapter.Enabled {
					icon.SetResource(theme.ConfirmIcon())
				} else {
					icon.SetResource(theme.CancelIcon())
				}
			}
		},
	)

	selectedIdx := -1
	adapterList.OnSelected = func(id widget.ListItemID) {
		selectedIdx = int(id)
	}

	// ── Delete / Toggle buttons ──
	delBtn := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
		if selectedIdx < 0 || selectedIdx >= len(adapterNames) {
			return
		}
		_ = cfg.RemoveIMAdapter(adapterNames[selectedIdx])
		_ = cfg.Save()
		selectedIdx = -1
		s.rebuildIMTab()
	})
	toggleBtn := widget.NewButtonWithIcon("Toggle On/Off", theme.MediaPlayIcon(), func() {
		if selectedIdx < 0 || selectedIdx >= len(adapterNames) {
			return
		}
		name := adapterNames[selectedIdx]
		a := cfg.IM.Adapters[name]
		_ = cfg.SetIMAdapterEnabled(name, !a.Enabled)
		_ = cfg.Save()
		s.rebuildIMTab()
	})

	// ── Add adapter: platform → dynamic fields ──
	platforms := []string{"qq", "telegram", "discord", "feishu", "dingtalk", "slack", "wechat", "wecom", "whatsapp", "mattermost", "signal", "irc", "matrix", "nostr", "twitch"}
	platformSelect := widget.NewSelect(platforms, nil)
	platformSelect.PlaceHolder = "Select platform..."

	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "e.g. my-bot"

	// Dynamic fields container — rebuilt when platform changes.
	fieldsBox := container.NewVBox()
	fieldEntries := make(map[string]*widget.Entry)

	platformSelect.OnChanged = func(p string) {
		fieldsBox.Objects = nil
		fieldEntries = make(map[string]*widget.Entry)
		for _, fieldName := range platformFields(p) {
			entry := widget.NewEntry()
			entry.PlaceHolder = fieldName
			entry.Wrapping = fyne.TextWrapWord
			fieldEntries[fieldName] = entry
			fieldsBox.Add(widget.NewForm(&widget.FormItem{Text: fieldName, Widget: entry}))
		}
		fieldsBox.Refresh()
	}

	addBtn := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() {
		name := strings.TrimSpace(nameEntry.Text)
		plat := platformSelect.Selected
		if name == "" || plat == "" {
			imStatus.SetText("Name and platform required")
			return
		}
		extra := make(map[string]interface{})
		for k, e := range fieldEntries {
			if strings.TrimSpace(e.Text) != "" {
				extra[k] = strings.TrimSpace(e.Text)
			}
		}
		cfg.IM.Enabled = true
		err := cfg.AddIMAdapter(name, config.IMAdapterConfig{
			Enabled:  true,
			Platform: plat,
			Extra:    extra,
		})
		if err != nil {
			imStatus.SetText("Error: " + err.Error())
			return
		}
		_ = cfg.Save()
		nameEntry.SetText("")
		platformSelect.SetSelected("")
		for _, e := range fieldEntries {
			e.SetText("")
		}
		imStatus.SetText("Added: " + name)
		s.rebuildIMTab()
	})

	// ── Layout ──
	return container.NewVScroll(container.NewVBox(
		widget.NewCard("Adapters", "", adapterList),
		container.NewHBox(toggleBtn, delBtn),
		widget.NewCard("Add Adapter", "", container.NewVBox(
			widget.NewForm(
				&widget.FormItem{Text: "Platform", Widget: platformSelect},
				&widget.FormItem{Text: "Name", Widget: nameEntry},
			),
			fieldsBox,
			addBtn,
		)),
		imStatus,
	))
}

// platformFields returns the Extra field names required for each platform.
func platformFields(platform string) []string {
	switch platform {
	case "qq":
		return []string{"appid", "appsecret"}
	case "telegram":
		return []string{"bot_token"}
	case "discord":
		return []string{"token"}
	case "feishu":
		return []string{"app_id", "app_secret"}
	case "dingtalk":
		return []string{"app_key", "app_secret"}
	case "slack":
		return []string{"bot_token", "app_token"}
	case "wechat":
		return []string{"bot_token"}
	case "wecom":
		return []string{"bot_id", "secret"}
	case "whatsapp":
		return []string{} // uses QR pairing
	case "mattermost":
		return []string{"url", "token"}
	case "signal":
		return []string{"account", "base_url"}
	case "irc":
		return []string{"host", "nick", "channels"}
	case "matrix":
		return []string{"homeserver", "access_token"}
	case "nostr":
		return []string{"private_key", "relays"}
	case "twitch":
		return []string{"nick", "token", "channels"}
	default:
		return nil
	}
}

func (s *Sidebar) rebuildIMTab() {
	if s.tabs == nil {
		return
	}
	// Rebuild the IM tab (index 2).
	if len(s.tabs.Items) >= 3 {
		s.tabs.Items[2].Content = s.buildIMTab()
		s.tabs.Refresh()
	}
}

func platformDisplayName(platform string) string {
	switch platform {
	case "qq":
		return "QQ"
	case "telegram":
		return "Telegram"
	case "discord":
		return "Discord"
	case "feishu":
		return "Feishu"
	case "dingtalk":
		return "DingTalk"
	case "slack":
		return "Slack"
	case "wechat":
		return "WeChat"
	case "wecom":
		return "WeCom"
	case "whatsapp":
		return "WhatsApp"
	case "mattermost":
		return "Mattermost"
	default:
		return platform
	}
}

func sortedAdapterNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.IM.Adapters))
	for name := range cfg.IM.Adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// showImpersonateDialog shows a dialog to select impersonation preset.
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
	versionEntry.SetPlaceHolder("Custom version (optional)")
	if s.app.cfg != nil && s.app.cfg.Impersonation.CustomVersion != "" {
		versionEntry.SetText(s.app.cfg.Impersonation.CustomVersion)
	}

	form := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "Identity", Widget: selectEntry},
			{Text: "Version", Widget: versionEntry},
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

	d := dialog.NewCustomConfirm("Impersonation", "Apply", "Cancel", form, func(ok bool) {
		if ok {
			form.OnSubmit()
		}
	}, s.app.window)
	d.Resize(fyne.NewSize(400, 250))
	d.Show()
}
