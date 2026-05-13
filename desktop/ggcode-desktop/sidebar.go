package main

import (
	"fmt"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/session"
)

// Sidebar renders the right panel with tabs: Context, Provider.
type Sidebar struct {
	app    *App
	bridge *AgentBridge
	ui     *UIState
	tabs   *container.AppTabs

	// Widgets that need live updates.
	contextLabel *widget.Label
	tokenLabel   *widget.Label
	tokenBar     *widget.ProgressBar
	modelLabel   *widget.Label
	sessionList  *widget.List
	sessions     []sessionMeta
}

type sessionMeta struct {
	ID   string
	Name string
	Time time.Time
}

func NewSidebar(app *App, bridge *AgentBridge, ui *UIState) *Sidebar {
	return &Sidebar{app: app, bridge: bridge, ui: ui}
}

// Render returns the fyne widget tree for this sidebar.
func (s *Sidebar) Render() fyne.CanvasObject {
	s.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Context", theme.InfoIcon(), s.buildContextTab()),
		container.NewTabItemWithIcon("Provider", theme.ComputerIcon(), s.buildProviderTab()),
	)
	return s.tabs
}

// RefreshStats updates the context usage display.
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
	s.modelLabel.Refresh()
	s.contextLabel.Refresh()
	s.tokenLabel.Refresh()
	s.tokenBar.Refresh()
}

// ── Context tab ──────────────────────────────────────

func (s *Sidebar) buildContextTab() fyne.CanvasObject {
	resolved := s.bridge.Resolved()

	// Model info section.
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
			// Border layout: center=label, right=timeLabel
			nameLabel := box.Objects[0].(*widget.Label)
			timeLabel := box.Objects[1].(*widget.Label)
			sess := s.sessions[id]
			nameLabel.SetText(sess.Name)
			timeLabel.SetText(sess.Time.Format("01-02 15:04"))
		},
	)
	s.sessionList.Resize(fyne.NewSize(200, 200))

	sessionCard := widget.NewCard("Sessions", "", s.sessionList)

	return container.NewVScroll(container.NewVBox(
		infoCard,
		statsCard,
		sessionCard,
	))
}

func (s *Sidebar) loadSessions() {
	workspace := s.app.dc.WorkDir

	store, err := session.NewDefaultStore()
	if err != nil {
		s.sessions = nil
		return
	}
	allSessions, err := store.List()
	if err != nil {
		s.sessions = nil
		return
	}

	// Filter by current workspace.
	var filtered []*session.Session
	for _, sess := range allSessions {
		if sess.Workspace == workspace {
			filtered = append(filtered, sess)
		}
	}

	// Sort newest first.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})

	// Take latest 5.
	if len(filtered) > 5 {
		filtered = filtered[:5]
	}

	s.sessions = make([]sessionMeta, 0, len(filtered))
	for _, sess := range filtered {
		name := sess.Title
		if name == "" {
			name = sess.ID
			if len(name) > 8 {
				name = name[:8]
			}
		}
		if len(name) > 50 {
			name = name[:50] + "..."
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

	// Vendor selection.
	vendorNames := make([]string, 0, len(cfg.Vendors))
	for name := range cfg.Vendors {
		vendorNames = append(vendorNames, name)
	}
	vendorSelect := widget.NewSelect(vendorNames, nil)
	vendorSelect.SetSelected(cfg.Vendor)

	// Endpoint selection.
	epSelect := widget.NewSelect([]string{}, nil)
	updateEndpoints := func(vendor string) {
		if v, ok := cfg.Vendors[vendor]; ok {
			eps := make([]string, 0, len(v.Endpoints))
			for name := range v.Endpoints {
				eps = append(eps, name)
			}
			epSelect.Options = eps
			epSelect.Refresh()
		}
	}
	updateEndpoints(cfg.Vendor)
	epSelect.SetSelected(cfg.Endpoint)

	// Model entry.
	modelEntry := widget.NewEntry()
	modelEntry.SetPlaceHolder("Model name")
	modelEntry.SetText(cfg.Model)

	// Apply button.
	applyBtn := widget.NewButtonWithIcon("Apply", theme.ConfirmIcon(), func() {
		cfg.Vendor = vendorSelect.Selected
		cfg.Endpoint = epSelect.Selected
		cfg.Model = modelEntry.Text
		_ = cfg.Save()

		// Re-init with new settings.
		s.app.startChat()
	})
	applyBtn.Importance = widget.HighImportance

	return container.NewVScroll(container.NewVBox(
		widget.NewCard("Provider", "", container.NewVBox(
			widget.NewForm(
				&widget.FormItem{Text: "Vendor", Widget: vendorSelect},
				&widget.FormItem{Text: "Endpoint", Widget: epSelect},
				&widget.FormItem{Text: "Model", Widget: modelEntry},
			),
			applyBtn,
		)),
	))
}
