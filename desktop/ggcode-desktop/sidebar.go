package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
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

	// IM tab state
	imVBox          *fyne.Container
	imAdapterNames  *[]string
	imDetailRefresh func()
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

	sessionHeader := widget.NewLabel("Sessions")
	sessionHeader.TextStyle = fyne.TextStyle{Bold: true}
	topSection := container.NewVBox(infoCard, statsCard, container.NewPadded(sessionHeader))
	return container.NewBorder(topSection, nil, nil, nil, s.sessionList)
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

	var filtered []*session.Session
	for _, sess := range allSessions {
		if sess.Workspace == workspace {
			filtered = append(filtered, sess)
		}
	}
	logf("sidebar", "loadSessions: workspace=%s filtered=%d", workspace, len(filtered))

	if len(filtered) == 0 {
		filtered = allSessions
	}

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
	})
	s.epSelect = widget.NewSelect([]string{}, func(ep string) {
		s.onEndpointChange(s.vendorSelect.Selected, ep)
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
				s.modelLoading.SetText("Error: " + err.Error())
				return
			}
			s.modelSelect.Options = models
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

	// Ensure IM map initialized.
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}

	// Sorted adapter names.
	adapterNames := sortedAdapterNames(cfg)

	// Status label for feedback.
	imStatus := widget.NewLabel("")

	// ── Adapter list ──
	adapterList := widget.NewList(
		func() int { return len(adapterNames) },
		func() fyne.CanvasObject {
			nameLbl := widget.NewLabel("adapter")
			nameLbl.Wrapping = fyne.TextWrapWord
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
			var nameLbl *widget.Label
			var platLbl *widget.Label
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

	// Track selected adapter index.
	selectedIdx := -1
	adapterList.OnSelected = func(id widget.ListItemID) {
		selectedIdx = int(id)
	}

	// ── Detail panel for selected adapter ──
	detailName := widget.NewLabel("")
	detailName.TextStyle = fyne.TextStyle{Bold: true}
	detailPlatform := widget.NewLabel("")
	detailTransport := widget.NewLabel("")
	detailCommand := widget.NewEntry()
	detailCommand.PlaceHolder = "Command (optional)"
	detailCommand.Wrapping = fyne.TextWrapWord
	detailEnabled := widget.NewCheck("Enabled", nil)

	refreshDetail := func() {
		if selectedIdx < 0 || selectedIdx >= len(adapterNames) {
			detailName.SetText("Select an adapter")
			detailPlatform.SetText("")
			detailTransport.SetText("")
			detailCommand.SetText("")
			detailEnabled.SetChecked(false)
			return
		}
		name := adapterNames[selectedIdx]
		adapter := cfg.IM.Adapters[name]
		detailName.SetText(name)
		detailPlatform.SetText("Platform: " + platformDisplayName(adapter.Platform))
		detailTransport.SetText("Transport: " + adapter.Transport)
		detailCommand.SetText(adapter.Command)
		detailEnabled.SetChecked(adapter.Enabled)
	}

	// Save detail changes back to config.
	saveDetail := func() {
		if selectedIdx < 0 || selectedIdx >= len(adapterNames) {
			return
		}
		name := adapterNames[selectedIdx]
		adapter := cfg.IM.Adapters[name]
		adapter.Command = detailCommand.Text
		adapter.Enabled = detailEnabled.Checked
		cfg.IM.Adapters[name] = adapter
		_ = cfg.Save()
		imStatus.SetText("Saved")
	}

	saveDetailBtn := widget.NewButton("Save Changes", func() {
		saveDetail()
	})

	// ── Action buttons ──
	delBtn := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
		if selectedIdx < 0 || selectedIdx >= len(adapterNames) {
			return
		}
		_ = cfg.RemoveIMAdapter(adapterNames[selectedIdx])
		_ = cfg.Save()
		selectedIdx = -1
		s.rebuildIMTab()
	})

	toggleBtn := widget.NewButtonWithIcon("Toggle", theme.MediaPlayIcon(), func() {
		if selectedIdx < 0 || selectedIdx >= len(adapterNames) {
			return
		}
		name := adapterNames[selectedIdx]
		adapter := cfg.IM.Adapters[name]
		_ = cfg.SetIMAdapterEnabled(name, !adapter.Enabled)
		_ = cfg.Save()
		s.rebuildIMTab()
	})

	// ── Add adapter form ──
	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "e.g. my-qq-bot"

	platforms := []string{"qq", "telegram", "discord", "feishu", "dingtalk", "slack", "wechat", "wecom", "whatsapp", "mattermost"}
	platformSelect := widget.NewSelect(platforms, nil)
	platformSelect.PlaceHolder = "Select platform..."

	transports := []string{"stdio", "webhook"}
	transportSelect := widget.NewSelect(transports, nil)
	transportSelect.SetSelected("stdio")

	cmdEntry := widget.NewEntry()
	cmdEntry.PlaceHolder = "Command (optional, for stdio transport)"

	addBtn := widget.NewButtonWithIcon("Add Adapter", theme.ContentAddIcon(), func() {
		name := strings.TrimSpace(nameEntry.Text)
		if name == "" || platformSelect.Selected == "" {
			imStatus.SetText("Name and platform required")
			return
		}
		cfg.IM.Enabled = true
		err := cfg.AddIMAdapter(name, config.IMAdapterConfig{
			Enabled:   true,
			Platform:  platformSelect.Selected,
			Transport: transportSelect.Selected,
			Command:   cmdEntry.Text,
			Extra:     map[string]interface{}{},
		})
		if err != nil {
			imStatus.SetText("Error: " + err.Error())
			return
		}
		_ = cfg.Save()
		nameEntry.SetText("")
		cmdEntry.SetText("")
		imStatus.SetText("Adapter " + name + " added")
		s.rebuildIMTab()
	})

	// ── Layout ──
	detailCard := widget.NewCard("Adapter Detail", "", container.NewVBox(
		detailName,
		container.NewHBox(detailPlatform, layout.NewSpacer(), detailTransport),
		detailCommand,
		detailEnabled,
		container.NewHBox(saveDetailBtn, toggleBtn, delBtn),
	))

	addCard := widget.NewCard("Add Adapter", "", container.NewVBox(
		widget.NewForm(
			&widget.FormItem{Text: "Name", Widget: nameEntry},
			&widget.FormItem{Text: "Platform", Widget: platformSelect},
			&widget.FormItem{Text: "Transport", Widget: transportSelect},
			&widget.FormItem{Text: "Command", Widget: cmdEntry},
		),
		addBtn,
	))

	// Full tab layout: adapter list + detail + add form.
	vbox := container.NewVBox(
		widget.NewCard("Adapters", "", adapterList),
		detailCard,
		addCard,
		imStatus,
	)

	s.imVBox = vbox
	s.imAdapterNames = &adapterNames
	s.imDetailRefresh = refreshDetail

	return container.NewVScroll(vbox)
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
