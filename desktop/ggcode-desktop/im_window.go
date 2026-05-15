package main

import (
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

// imAdapterEntry groups adapter info for display.
type imAdapterEntry struct {
	Name      string
	Platform  string
	Enabled   bool
	Workspace string
	IsCurrent bool
	Healthy   bool
	Muted     bool
	ChannelID string
}

// showIMWindow opens the IM Settings window (singleton).
func (a *App) showIMWindow() {
	if a.imWindow != nil {
		a.imWindow.Show()
		a.imWindow.RequestFocus()
		return
	}
	w := a.fyneApp.NewWindow("IM Settings")
	w.Resize(fyne.NewSize(850, 680))
	w.SetOnClosed(func() { a.imWindow = nil })
	a.imWindow = w

	// Build content.
	content := a.buildIMContent(w)
	w.SetContent(content)
	w.Show()
}

func (a *App) buildIMContent(w fyne.Window) fyne.CanvasObject {
	cfg := a.cfg
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}

	// ── Header ──
	headerTitle := widget.NewLabelWithStyle("IM Settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	currentWS := a.dc.WorkDir
	wsLabel := widget.NewLabel("Workspace: " + currentWS)
	wsLabel.Wrapping = fyne.TextWrapWord
	wsLabel.TextStyle = fyne.TextStyle{Italic: true}

	// ── Refresh function ──
	var scroll *container.Scroll

	refresh := func() {
		body := a.buildIMBody(w, cfg)
		if scroll != nil {
			scroll.Content = body
			scroll.Refresh()
		}
	}

	// ── Action buttons ──
	addBtn := widget.NewButtonWithIcon("Add Adapter", theme.ContentAddIcon(), func() {
		a.showAddAdapterDialog(w, cfg, refresh)
	})
	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		refresh()
	})

	toolbar := container.NewHBox(
		layout.NewSpacer(),
		refreshBtn,
		addBtn,
	)

	// ── Body ──
	body := a.buildIMBody(w, cfg)
	scroll = container.NewVScroll(body)

	header := container.NewVBox(
		container.NewHBox(headerTitle, layout.NewSpacer()),
		wsLabel,
		toolbar,
		widget.NewSeparator(),
	)

	return container.NewBorder(header, nil, nil, nil, scroll)
}

// buildIMBody creates the scrollable adapter card list.
func (a *App) buildIMBody(w fyne.Window, cfg *config.Config) *fyne.Container {
	entries := a.imAdapterEntries()
	currentWS := a.dc.WorkDir

	// Group entries.
	var currentGroup, otherGroup, unboundGroup, disabledGroup []imAdapterEntry
	for _, e := range entries {
		if !e.Enabled {
			disabledGroup = append(disabledGroup, e)
		} else if e.IsCurrent {
			currentGroup = append(currentGroup, e)
		} else if e.Workspace != "" {
			otherGroup = append(otherGroup, e)
		} else {
			unboundGroup = append(unboundGroup, e)
		}
	}

	var cards []fyne.CanvasObject

	// ── Section: Bound to this workspace ──
	if len(currentGroup) > 0 {
		cards = append(cards, a.sectionHeader("Bound to this workspace", theme.ConfirmIcon()))
		for _, e := range currentGroup {
			cards = append(cards, a.adapterCard(w, e, currentWS, func() {}))
		}
	}

	// ── Section: Bound to other workspaces ──
	if len(otherGroup) > 0 {
		cards = append(cards, a.sectionHeader("Other workspaces", theme.MailForwardIcon()))
		for _, e := range otherGroup {
			cards = append(cards, a.adapterCard(w, e, currentWS, func() {}))
		}
	}

	// ── Section: Unbound ──
	if len(unboundGroup) > 0 {
		cards = append(cards, a.sectionHeader("Unbound adapters", theme.ComputerIcon()))
		for _, e := range unboundGroup {
			cards = append(cards, a.adapterCard(w, e, currentWS, func() {}))
		}
	}

	// ── Section: Disabled ──
	if len(disabledGroup) > 0 {
		cards = append(cards, a.sectionHeader("Disabled", theme.CancelIcon()))
		for _, e := range disabledGroup {
			cards = append(cards, a.adapterCard(w, e, currentWS, func() {}))
		}
	}

	if len(cards) == 0 {
		emptyLabel := widget.NewLabel("No adapters configured.\nClick 'Add Adapter' to get started.")
		emptyLabel.Alignment = fyne.TextAlignCenter
		cards = append(cards, emptyLabel)
	}

	return container.NewVBox(cards...)
}

// sectionHeader returns a section title row.
func (a *App) sectionHeader(title string, icon fyne.Resource) fyne.CanvasObject {
	lbl := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	ic := widget.NewIcon(icon)
	return container.NewHBox(ic, lbl)
}

// adapterCard creates a card for a single adapter.
func (a *App) adapterCard(w fyne.Window, e imAdapterEntry, currentWS string, onRefresh func()) fyne.CanvasObject {
	// Left: name + platform
	nameLbl := widget.NewLabelWithStyle(e.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	platLbl := widget.NewLabel(platformDisplayName(e.Platform))

	// Status indicator
	var statusText, statusColor string
	switch {
	case !e.Enabled:
		statusText = "Disabled"
		statusColor = "danger"
	case e.Muted:
		statusText = "Muted"
		statusColor = "warning"
	case e.IsCurrent:
		if e.Healthy {
			statusText = "Connected"
			statusColor = "success"
		} else {
			statusText = "Active"
			statusColor = "success"
		}
	case e.Workspace != "":
		statusText = "In use"
		statusColor = "info"
	default:
		statusText = "Ready"
		statusColor = "info"
	}

	statusLbl := widget.NewLabel(statusText)
	statusLbl.TextStyle = fyne.TextStyle{Bold: true}
	_ = statusColor // TODO: apply color

	// Workspace binding info
	var bindText string
	if e.IsCurrent {
		bindText = "This workspace"
	} else if e.Workspace != "" {
		// Show relative or shortened path
		if strings.HasPrefix(e.Workspace, currentWS) {
			bindText = "This workspace"
		} else {
			parts := strings.Split(e.Workspace, "/")
			if len(parts) > 2 {
				bindText = "Bound to: .../" + parts[len(parts)-2] + "/" + parts[len(parts)-1]
			} else {
				bindText = "Bound to: " + e.Workspace
			}
		}
	} else {
		bindText = "Not bound"
	}
	bindLbl := widget.NewLabel(bindText)
	bindLbl.TextStyle = fyne.TextStyle{Italic: true}

	// Channel info
	var chText string
	if e.ChannelID != "" {
		if len(e.ChannelID) > 20 {
			chText = "Channel: " + e.ChannelID[:20] + "..."
		} else {
			chText = "Channel: " + e.ChannelID
		}
	}
	var chLbl *widget.Label
	if chText != "" {
		chLbl = widget.NewLabel(chText)
		chLbl.TextStyle = fyne.TextStyle{Italic: true}
	}

	// Left info column
	var infoChildren []fyne.CanvasObject
	infoChildren = append(infoChildren, nameLbl, container.NewHBox(platLbl, layout.NewSpacer(), statusLbl), bindLbl)
	if chLbl != nil {
		infoChildren = append(infoChildren, chLbl)
	}
	infoCol := container.NewVBox(infoChildren...)

	// ── Action row (horizontal, compact icon buttons) ──
	var actions []fyne.CanvasObject

	// Toggle
	if e.Enabled {
		actions = append(actions, widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			_ = a.cfg.SetIMAdapterEnabled(e.Name, false)
			_ = a.cfg.Save()
			if a.imManager != nil {
				_ = a.imManager.DisableBinding(e.Name)
			}
			a.refreshIMWindow()
		}))
	} else {
		actions = append(actions, widget.NewButtonWithIcon("", theme.ConfirmIcon(), func() {
			_ = a.cfg.SetIMAdapterEnabled(e.Name, true)
			_ = a.cfg.Save()
			if a.imManager != nil {
				_ = a.imManager.EnableBinding(e.Name)
			}
			a.refreshIMWindow()
		}))
	}

	// Mute/Unmute
	if e.Enabled && e.IsCurrent {
		if e.Muted {
			actions = append(actions, widget.NewButtonWithIcon("", theme.VolumeUpIcon(), func() {
				if a.imManager != nil {
					_ = a.imManager.UnmuteBinding(e.Name)
				}
				a.refreshIMWindow()
			}))
		} else {
			actions = append(actions, widget.NewButtonWithIcon("", theme.VolumeMuteIcon(), func() {
				if a.imManager != nil {
					_ = a.imManager.MuteBinding(e.Name)
				}
				a.refreshIMWindow()
			}))
		}
	}

	// Bind/Rebind
	if !e.IsCurrent {
		actions = append(actions, widget.NewButtonWithIcon("", theme.MailForwardIcon(), func() {
			if a.imManager == nil {
				dialog.ShowInformation("Bind", "IM manager is not available yet.", w)
				return
			}
			if e.Workspace != "" {
				_ = a.imManager.UnbindAdapter(e.Name)
			}
			a.imManager.BindSession(im.SessionBinding{Workspace: a.dc.WorkDir})
			_, _ = a.imManager.BindChannel(im.ChannelBinding{
				Workspace: a.dc.WorkDir,
				Platform:  im.Platform(e.Platform),
				Adapter:   e.Name,
			})
			a.refreshIMWindow()
		}))
	}

	// Delete (last, smaller, less prominent)
	delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Delete", fmt.Sprintf("Delete adapter '%s'?", e.Name), func(ok bool) {
			if !ok {
				return
			}
			_ = a.cfg.RemoveIMAdapter(e.Name)
			_ = a.cfg.Save()
			a.refreshIMWindow()
		}, w)
	})
	delBtn.Importance = widget.DangerImportance
	actions = append(actions, delBtn)

	actionRow := container.NewHBox(actions...)
	btnCol := container.NewVBox(actionRow)

	// Card row: info | buttons
	row := container.NewBorder(nil, nil, nil, btnCol, infoCol)

	// Visual separation with background color hint via card
	return widget.NewCard("", "", container.NewPadded(row))
}

// refreshIMWindow rebuilds the IM window content.
func (a *App) refreshIMWindow() {
	if a.imWindow == nil {
		return
	}
	fyne.Do(func() {
		content := a.buildIMContent(a.imWindow)
		a.imWindow.SetContent(content)
	})
}

// showAddAdapterDialog opens a dialog to add a new adapter.
func (a *App) showAddAdapterDialog(w fyne.Window, cfg *config.Config, onDone func()) {
	platforms := []string{"qq", "telegram", "discord", "feishu", "dingtalk", "slack", "wechat", "wecom", "whatsapp", "mattermost", "signal", "irc", "matrix", "nostr", "twitch"}
	platformSelect := widget.NewSelect(platforms, nil)
	platformSelect.PlaceHolder = "Select platform..."

	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "e.g. my-bot"

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

	statusLabel := widget.NewLabel("")

	form := container.NewVBox(
		widget.NewForm(
			&widget.FormItem{Text: "Platform", Widget: platformSelect},
			&widget.FormItem{Text: "Name", Widget: nameEntry},
		),
		fieldsBox,
		statusLabel,
	)

	d := dialog.NewCustomConfirm("Add Adapter", "Add", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		name := strings.TrimSpace(nameEntry.Text)
		plat := platformSelect.Selected
		if name == "" || plat == "" {
			statusLabel.SetText("Name and platform required")
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
			statusLabel.SetText("Error: " + err.Error())
			return
		}
		_ = cfg.Save()
		if a.imManager != nil {
			adapters := make(map[string]bool)
			for n, acfg := range cfg.IM.Adapters {
				adapters[n] = acfg.Enabled
			}
			a.imManager.ApplyAdapterConfig(adapters)
		}
		onDone()
	}, w)
	d.Resize(fyne.NewSize(500, 400))
	d.Show()
}

// imAdapterEntries returns sorted adapter entries from config + imManager state.
func (a *App) imAdapterEntries() []imAdapterEntry {
	cfg := a.cfg
	if cfg.IM.Adapters == nil {
		return nil
	}
	currentWS := ""
	if a.dc != nil {
		currentWS = a.dc.WorkDir
	}

	bindingWorkspace := make(map[string]string)
	bindingChannel := make(map[string]string)
	bindingMuted := make(map[string]bool)
	adapterHealthy := make(map[string]bool)
	if a.imManager != nil {
		snap := a.imManager.Snapshot()
		for _, b := range snap.CurrentBindings {
			bindingWorkspace[b.Adapter] = b.Workspace
			bindingChannel[b.Adapter] = b.ChannelID
			bindingMuted[b.Adapter] = b.Muted
		}
		for _, b := range snap.DisabledBindings {
			if _, exists := bindingWorkspace[b.Adapter]; !exists {
				bindingWorkspace[b.Adapter] = b.Workspace
				bindingChannel[b.Adapter] = b.ChannelID
			}
		}
		for _, s := range snap.Adapters {
			adapterHealthy[s.Name] = s.Healthy
		}
	}

	var current, other, unbound, disabled []imAdapterEntry
	for name, ac := range cfg.IM.Adapters {
		ws := bindingWorkspace[name]
		e := imAdapterEntry{
			Name:      name,
			Platform:  ac.Platform,
			Enabled:   ac.Enabled,
			Workspace: ws,
			IsCurrent: ws == currentWS && ws != "",
			Healthy:   adapterHealthy[name],
			Muted:     bindingMuted[name],
			ChannelID: bindingChannel[name],
		}

		if !ac.Enabled {
			disabled = append(disabled, e)
		} else if e.IsCurrent {
			current = append(current, e)
		} else if e.Workspace != "" {
			other = append(other, e)
		} else {
			unbound = append(unbound, e)
		}
	}

	sort.Slice(current, func(i, j int) bool { return current[i].Name < current[j].Name })
	sort.Slice(other, func(i, j int) bool { return other[i].Name < other[j].Name })
	sort.Slice(unbound, func(i, j int) bool { return unbound[i].Name < unbound[j].Name })
	sort.Slice(disabled, func(i, j int) bool { return disabled[i].Name < disabled[j].Name })

	var result []imAdapterEntry
	result = append(result, current...)
	result = append(result, other...)
	result = append(result, unbound...)
	result = append(result, disabled...)
	return result
}

// platformDisplayName returns a user-friendly platform name.
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
	case "signal":
		return "Signal"
	case "irc":
		return "IRC"
	case "matrix":
		return "Matrix"
	case "nostr":
		return "Nostr"
	case "twitch":
		return "Twitch"
	default:
		return strings.Title(platform)
	}
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
		return []string{}
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

// sortedAdapterNames returns adapter names in sorted order.
func sortedAdapterNames(cfg *config.Config) []string {
	if cfg.IM.Adapters == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.IM.Adapters))
	for n := range cfg.IM.Adapters {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// blankLine returns a simple spacer.
func blankLine() fyne.CanvasObject {
	return canvas.NewRectangle(theme.BackgroundColor())
}
