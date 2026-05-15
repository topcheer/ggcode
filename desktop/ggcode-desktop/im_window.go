package main

import (
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

// ──────────────────────────── Data types ────────────────────────────

// imAdapterEntry holds adapter display info.
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

// ──────────────────────────── Platform metadata ────────────────────────────

type platformMeta struct {
	DisplayName string
	Fields      []platformField
}

type platformField struct {
	Key         string
	Label       string
	Placeholder string
}

var platformRegistry = map[string]platformMeta{
	"qq":        {DisplayName: "QQ", Fields: []platformField{{"appid", "App ID", "QQ app ID"}, {"appsecret", "App Secret", "QQ app secret"}}},
	"telegram":  {DisplayName: "Telegram", Fields: []platformField{{"bot_token", "Bot Token", "123456:ABC-DEF..."}}},
	"discord":   {DisplayName: "Discord", Fields: []platformField{{"token", "Bot Token", "Discord bot token"}}},
	"feishu":    {DisplayName: "Feishu", Fields: []platformField{{"app_id", "App ID", "cli_xxx"}, {"app_secret", "App Secret", "Feishu app secret"}}},
	"dingtalk":  {DisplayName: "DingTalk", Fields: []platformField{{"app_key", "App Key", "dingxxx"}, {"app_secret", "App Secret", "DingTalk app secret"}}},
	"slack":     {DisplayName: "Slack", Fields: []platformField{{"bot_token", "Bot Token", "xoxb-xxx"}, {"app_token", "App Token", "xapp-xxx"}}},
	"wechat":    {DisplayName: "WeChat", Fields: []platformField{{"bot_token", "Bot Token", "WeChat bot token"}}},
	"wecom":     {DisplayName: "WeCom", Fields: []platformField{{"bot_id", "Bot ID", "WeCom bot ID"}, {"secret", "Secret", "WeCom secret"}}},
	"whatsapp":  {DisplayName: "WhatsApp", Fields: []platformField{}},
	"mattermost": {DisplayName: "Mattermost", Fields: []platformField{{"url", "Server URL", "https://mm.example.com"}, {"token", "Access Token", "mattermost token"}}},
	"signal":    {DisplayName: "Signal", Fields: []platformField{{"account", "Phone Number", "+1234567890"}, {"base_url", "Signal CLI URL", "http://localhost:8080"}}},
	"irc":       {DisplayName: "IRC", Fields: []platformField{{"host", "Server", "irc.libera.chat:6697"}, {"nick", "Nickname", "my-bot"}, {"channels", "Channels", "#channel1,#channel2"}}},
	"matrix":    {DisplayName: "Matrix", Fields: []platformField{{"homeserver", "Homeserver", "https://matrix.org"}, {"access_token", "Access Token", "syt_xxx"}}},
	"nostr":     {DisplayName: "Nostr", Fields: []platformField{{"private_key", "Private Key", "nsec1..."}, {"relays", "Relays", "wss://relay.damus.io"}}},
	"twitch":    {DisplayName: "Twitch", Fields: []platformField{{"nick", "Nickname", "bot_name"}, {"token", "OAuth Token", "oauth:xxx"}, {"channels", "Channels", "#channel1,#channel2"}}},
}

func sortedPlatformKeys() []string {
	keys := make([]string, 0, len(platformRegistry))
	for k := range platformRegistry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func platformDisplayName(platform string) string {
	if meta, ok := platformRegistry[platform]; ok {
		return meta.DisplayName
	}
	return strings.Title(platform)
}

// ──────────────────────────── Show IM Settings ────────────────────────────

// showIMWindow opens the IM Settings window (singleton).
func (a *App) showIMWindow() {
	if a.imWindow != nil {
		a.imWindow.Show()
		a.imWindow.RequestFocus()
		return
	}
	if a.fyneApp == nil {
		return
	}
	w := a.fyneApp.NewWindow("IM Settings")
	w.Resize(fyne.NewSize(850, 620))
	w.SetOnClosed(func() { a.imWindow = nil })
	a.imWindow = w
	w.SetContent(a.buildIMDialogContent(w))
	w.Show()
}

// buildIMDialogContent creates the full content for the IM Settings dialog.
func (a *App) buildIMDialogContent(w fyne.Window) fyne.CanvasObject {
	cfg := a.cfg
	if cfg == nil || cfg.IM.Adapters == nil {
		empty := widget.NewLabel("No configuration loaded yet.")
		empty.Alignment = fyne.TextAlignCenter
		return empty
	}

	// ── Header row ──
	currentWS := ""
	if a.dc != nil {
		currentWS = a.dc.WorkDir
	}
	wsLabel := widget.NewLabel("Workspace: " + currentWS)
	wsLabel.Wrapping = fyne.TextWrapWord

	addBtn := widget.NewButtonWithIcon("Add Adapter", theme.ContentAddIcon(), func() {
		a.showAddAdapterDialog(w)
	})

	// Header: two rows. Top: title + add button. Bottom: workspace path.
	titleLbl := widget.NewLabelWithStyle("IM Settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewVBox(
		container.NewHBox(titleLbl, layout.NewSpacer(), addBtn),
		wsLabel,
		widget.NewSeparator(),
	)

	// ── Adapter list (scrollable) ──
	body := a.buildAdapterSections(w)
	scroll := container.NewVScroll(body)

	return container.NewBorder(header, nil, nil, nil, scroll)
}

// buildAdapterSections groups adapters and renders section cards.
func (a *App) buildAdapterSections(w fyne.Window) *fyne.Container {
	entries := a.imAdapterEntries()
	currentWS := ""
	if a.dc != nil {
		currentWS = a.dc.WorkDir
	}

	var current, other, unbound, disabled []imAdapterEntry
	for _, e := range entries {
		if !e.Enabled {
			disabled = append(disabled, e)
		} else if e.IsCurrent {
			current = append(current, e)
		} else if e.Workspace != "" {
			other = append(other, e)
		} else {
			unbound = append(unbound, e)
		}
	}

	var sections []fyne.CanvasObject
	addSection := func(title string, list []imAdapterEntry) {
		if len(list) == 0 {
			return
		}
		sections = append(sections, widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		for _, e := range list {
			sections = append(sections, a.buildAdapterRow(w, e, currentWS))
		}
	}

	addSection("This workspace", current)
	addSection("Other workspaces", other)
	addSection("Unbound", unbound)
	addSection("Disabled", disabled)

	if len(sections) == 0 {
		empty := widget.NewLabel("No adapters configured. Click 'Add Adapter' to get started.")
		empty.Alignment = fyne.TextAlignCenter
		sections = append(sections, empty)
	}

	return container.NewVBox(sections...)
}

// buildAdapterRow creates a single adapter row with info + actions.
func (a *App) buildAdapterRow(w fyne.Window, e imAdapterEntry, currentWS string) fyne.CanvasObject {
	// ── Info column ──
	nameLbl := widget.NewLabelWithStyle(e.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	platLbl := widget.NewLabel(platformDisplayName(e.Platform))

	// Status
	statusText := "Active"
	if !e.Enabled {
		statusText = "Disabled"
	} else if e.Muted {
		statusText = "Muted"
	} else if e.IsCurrent && e.Healthy {
		statusText = "Connected"
	}
	statusLbl := widget.NewLabel(statusText)

	// Binding
	bindText := "Not bound"
	if e.IsCurrent {
		bindText = "This workspace"
	} else if e.Workspace != "" {
		parts := strings.Split(e.Workspace, "/")
		if len(parts) >= 2 {
			bindText = ".../" + parts[len(parts)-2] + "/" + parts[len(parts)-1]
		} else {
			bindText = e.Workspace
		}
	}
	bindLbl := widget.NewLabel(bindText)
	bindLbl.TextStyle = fyne.TextStyle{Italic: true}

	// Channel
	var chLbl *widget.Label
	if e.ChannelID != "" {
		ch := e.ChannelID
		if len(ch) > 25 {
			ch = ch[:25] + "..."
		}
		chLbl = widget.NewLabel(ch)
		chLbl.TextStyle = fyne.TextStyle{Italic: true}
	}

	infoTop := container.NewHBox(nameLbl, platLbl, statusLbl)
	var infoChildren []fyne.CanvasObject
	infoChildren = append(infoChildren, infoTop, bindLbl)
	if chLbl != nil {
		infoChildren = append(infoChildren, chLbl)
	}
	infoCol := container.NewVBox(infoChildren...)

	// ── Actions ──
	var actions []fyne.CanvasObject

	// Enable/Disable
	if e.Enabled {
		actions = append(actions, widget.NewButton("Disable", func() {
			_ = a.cfg.SetIMAdapterEnabled(e.Name, false)
			_ = a.cfg.Save()
			if a.imManager != nil {
				_ = a.imManager.DisableBinding(e.Name)
			}
			a.refreshIMWindow()
		}))
	} else {
		actions = append(actions, widget.NewButton("Enable", func() {
			_ = a.cfg.SetIMAdapterEnabled(e.Name, true)
			_ = a.cfg.Save()
			if a.imManager != nil {
				_ = a.imManager.EnableBinding(e.Name)
			}
			a.refreshIMWindow()
		}))
	}

	// Mute/Unmute (only for enabled adapters bound to this workspace)
	if e.Enabled && e.IsCurrent {
		if e.Muted {
			actions = append(actions, widget.NewButton("Unmute", func() {
				if a.imManager != nil {
					_ = a.imManager.UnmuteBinding(e.Name)
				}
				a.refreshIMWindow()
			}))
		} else {
			actions = append(actions, widget.NewButton("Mute", func() {
				if a.imManager != nil {
					_ = a.imManager.MuteBinding(e.Name)
				}
				a.refreshIMWindow()
			}))
		}
	}

	// Bind to this workspace
	if !e.IsCurrent {
		actions = append(actions, widget.NewButton("Bind here", func() {
			if a.imManager == nil {
				dialog.ShowInformation("Bind", "IM manager is not available.", w)
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

	// Delete
	delBtn := widget.NewButton("Delete", func() {
		dialog.ShowConfirm("Delete", fmt.Sprintf("Delete adapter '%s'?", e.Name), func(ok bool) {
			if !ok {
				return
			}
			_ = a.cfg.RemoveIMAdapter(e.Name)
			_ = a.cfg.Save()
			if a.imManager != nil {
				_ = a.imManager.UnbindAdapter(e.Name)
			}
			a.refreshIMWindow()
		}, w)
	})
	delBtn.Importance = widget.DangerImportance
	actions = append(actions, delBtn)

	actionsRow := container.NewHBox(actions...)

	// ── Row: info (left) | actions (right) ──
	row := container.NewBorder(nil, nil, nil, actionsRow, infoCol)
	return widget.NewCard("", "", container.NewPadded(row))
}

// refreshIMWindow closes existing dialog and reopens.
func (a *App) refreshIMWindow() {
	if a.window == nil {
		return
	}
	// Re-show by closing and re-opening. Fyne doesn't have a clean way to
	// replace dialog content, so we just call showIMWindow which creates a new one.
	fyne.Do(func() {
		a.showIMWindow()
	})
}

// ──────────────────────────── Add Adapter Dialog ────────────────────────────

// showAddAdapterDialog opens a modal dialog to add a new adapter.
func (a *App) showAddAdapterDialog(w fyne.Window) {
	platformKeys := sortedPlatformKeys()
	platformLabels := make([]string, len(platformKeys))
	for i, k := range platformKeys {
		platformLabels[i] = platformRegistry[k].DisplayName
	}

	// Use a select with display names
	platformSelect := widget.NewSelect(platformLabels, nil)
	platformSelect.PlaceHolder = "Select platform..."

	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "e.g. my-bot"

	fieldsBox := container.NewVBox()
	fieldEntries := make(map[string]*widget.Entry)

	platformSelect.OnChanged = func(selected string) {
		fieldsBox.Objects = nil
		fieldEntries = make(map[string]*widget.Entry)
		// Find the platform key from display name
		var platKey string
		for k, meta := range platformRegistry {
			if meta.DisplayName == selected {
				platKey = k
				break
			}
		}
		if platKey == "" {
			fieldsBox.Refresh()
			return
		}
		meta := platformRegistry[platKey]
		if len(meta.Fields) == 0 {
			hint := widget.NewLabel("This platform requires scanning a QR code or link after adding.")
			hint.TextStyle = fyne.TextStyle{Italic: true}
			fieldsBox.Add(hint)
		}
		for _, f := range meta.Fields {
			entry := widget.NewEntry()
			entry.PlaceHolder = f.Placeholder
			entry.Wrapping = fyne.TextWrapWord
			fieldEntries[f.Key] = entry
			fieldsBox.Add(widget.NewForm(&widget.FormItem{Text: f.Label, Widget: entry}))
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
		selectedLabel := platformSelect.Selected
		if name == "" || selectedLabel == "" {
			statusLabel.SetText("Name and platform are required.")
			return
		}
		// Find platform key from display name
		var platKey string
		for k, meta := range platformRegistry {
			if meta.DisplayName == selectedLabel {
				platKey = k
				break
			}
		}
		if platKey == "" {
			statusLabel.SetText("Unknown platform.")
			return
		}
		extra := make(map[string]interface{})
		for k, e := range fieldEntries {
			if v := strings.TrimSpace(e.Text); v != "" {
				extra[k] = v
			}
		}
		a.cfg.IM.Enabled = true
		if err := a.cfg.AddIMAdapter(name, config.IMAdapterConfig{
			Enabled:  true,
			Platform: platKey,
			Extra:    extra,
		}); err != nil {
			statusLabel.SetText("Error: " + err.Error())
			return
		}
		_ = a.cfg.Save()
		if a.imManager != nil {
			adapters := make(map[string]bool)
			for n, acfg := range a.cfg.IM.Adapters {
				adapters[n] = acfg.Enabled
			}
			a.imManager.ApplyAdapterConfig(adapters)
		}
		a.refreshIMWindow()
	}, w)
	d.Resize(fyne.NewSize(480, 400))
	d.Show()
}

// ──────────────────────────── Data layer ────────────────────────────

// imAdapterEntries returns sorted adapter entries from config + imManager bindings.
func (a *App) imAdapterEntries() []imAdapterEntry {
	if a.cfg == nil || a.cfg.IM.Adapters == nil {
		return nil
	}
	currentWS := ""
	if a.dc != nil {
		currentWS = a.dc.WorkDir
	}

	// Read binding state from imManager
	bindingWorkspace := make(map[string]string)
	bindingChannel := make(map[string]string)
	bindingMuted := make(map[string]bool)
	adapterHealthy := make(map[string]bool)

	if a.imManager != nil {
		for _, b := range a.imManager.AllPersistedBindings() {
			bindingWorkspace[b.Adapter] = b.Workspace
			bindingChannel[b.Adapter] = b.ChannelID
			bindingMuted[b.Adapter] = b.Muted
		}
		for _, s := range a.imManager.Snapshot().Adapters {
			adapterHealthy[s.Name] = s.Healthy
		}
	}

	var current, other, unbound, disabled []imAdapterEntry
	for name, ac := range a.cfg.IM.Adapters {
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

// ─── Keep layout import used ───
var _ = layout.NewSpacer
