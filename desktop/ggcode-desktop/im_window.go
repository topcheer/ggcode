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
)

// imAdapterEntry groups adapter info for display.
type imAdapterEntry struct {
	Name       string
	Platform   string
	Enabled    bool
	Workspace  string // bound workspace path, empty if unbound
	IsCurrent  bool   // bound to current workspace
}

// showIMWindow opens the IM Settings window.
func (a *App) showIMWindow() {
	if a.imWindow != nil {
		a.imWindow.Show()
		a.imWindow.RequestFocus()
		return
	}
	w := a.fyneApp.NewWindow("IM Settings")
	w.Resize(fyne.NewSize(800, 600))
	w.SetOnClosed(func() { a.imWindow = nil })
	a.imWindow = w

	cfg := a.cfg
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}

	// ── Left: adapter list ──
	entries := a.imAdapterEntries()
	adapterList := widget.NewList(
		func() int { return len(entries) },
		func() fyne.CanvasObject {
			nameLbl := widget.NewLabel("adapter")
			nameLbl.TextStyle = fyne.TextStyle{Bold: true}
			platLbl := widget.NewLabel("platform")
			platLbl.TextStyle = fyne.TextStyle{Italic: true}
			wsLbl := widget.NewLabel("")
			wsLbl.TextStyle = fyne.TextStyle{Italic: true}
			statusIcon := widget.NewIcon(theme.ConfirmIcon())
			return container.NewBorder(nil, nil, statusIcon,
				container.NewVBox(platLbl, wsLbl),
				nameLbl,
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(entries) {
				return
			}
			e := entries[id]
			box := obj.(*fyne.Container)
			var nameLbl *widget.Label
			var platLbl, wsLbl *widget.Label
			var icon *widget.Icon
			for _, o := range box.Objects {
				switch v := o.(type) {
				case *widget.Label:
					if nameLbl == nil {
						nameLbl = v
					}
				case *widget.Icon:
					icon = v
				case *fyne.Container:
					for _, c := range v.Objects {
						if lbl, ok := c.(*widget.Label); ok {
							if platLbl == nil {
								platLbl = lbl
							} else {
								wsLbl = lbl
							}
						}
					}
				}
			}
			if nameLbl != nil {
				nameLbl.SetText(e.Name)
			}
			if platLbl != nil {
				platLbl.SetText(platformDisplayName(e.Platform))
			}
			if wsLbl != nil {
				if e.IsCurrent {
					wsLbl.SetText("↳ this workspace")
				} else if e.Workspace != "" {
					wsLbl.SetText("↳ " + shortPath(e.Workspace))
				} else {
					wsLbl.SetText("↳ unbound")
				}
			}
			if icon != nil {
				if e.Enabled {
					if e.IsCurrent {
						icon.SetResource(theme.ConfirmIcon())
					} else {
						icon.SetResource(theme.MailForwardIcon())
					}
				} else {
					icon.SetResource(theme.CancelIcon())
				}
			}
		},
	)

	// ── Right: detail panel ──
	detailCard := widget.NewCard("Details", "Select an adapter", container.NewVBox())
	addCard := a.buildIMAddCard(w, cfg, func() {
		entries = a.imAdapterEntries()
		adapterList.Refresh()
	})

	adapterList.OnSelected = func(id widget.ListItemID) {
		if id >= len(entries) {
			return
		}
		e := entries[id]
		detailCard.SetTitle(e.Name)

		statusText := "Enabled"
		if !e.Enabled {
			statusText = "Disabled"
		}
		bindText := "Unbound"
		if e.IsCurrent {
			bindText = "Bound to this workspace"
		} else if e.Workspace != "" {
			bindText = "Bound to: " + e.Workspace
		}

		statusLbl := widget.NewLabel(fmt.Sprintf("Status: %s", statusText))
		platformLbl := widget.NewLabel(fmt.Sprintf("Platform: %s", platformDisplayName(e.Platform)))
		bindLbl := widget.NewLabel(fmt.Sprintf("Binding: %s", bindText))
		bindLbl.Wrapping = fyne.TextWrapWord

		toggleBtn := widget.NewButton("Toggle On/Off", func() {
			_ = cfg.SetIMAdapterEnabled(e.Name, !e.Enabled)
			_ = cfg.Save()
			entries = a.imAdapterEntries()
			adapterList.Refresh()
			adapterList.OnSelected(id)
		})
		deleteBtn := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
			dialog.ShowConfirm("Delete Adapter", fmt.Sprintf("Delete adapter '%s'?", e.Name), func(ok bool) {
				if !ok {
					return
				}
				_ = cfg.RemoveIMAdapter(e.Name)
				_ = cfg.Save()
				entries = a.imAdapterEntries()
				adapterList.Refresh()
				detailCard.SetTitle("Details")
				detailCard.SetContent(container.NewVBox())
			}, w)
		})
		deleteBtn.Importance = widget.DangerImportance

		buttons := container.NewHBox(toggleBtn, deleteBtn)

		// Rebind button — show if adapter is bound to a different workspace or unbound
		var rebindBtn *widget.Button
		if !e.IsCurrent {
			rebindBtn = widget.NewButton("Bind to this workspace", func() {
				// TODO: actual bind logic when im runtime is available
				dialog.ShowInformation("Bind", fmt.Sprintf("Adapter '%s' will be bound to the current workspace when IM runtime is active.", e.Name), w)
			})
		}

		var content fyne.CanvasObject
		if rebindBtn != nil {
			content = container.NewVBox(platformLbl, statusLbl, bindLbl, buttons, rebindBtn)
		} else {
			content = container.NewVBox(platformLbl, statusLbl, bindLbl, buttons)
		}
		detailCard.SetContent(content)
	}

	// ── Layout ──
	leftPanel := container.NewBorder(nil, nil, nil, nil, adapterList)
	rightPanel := container.NewVBox(detailCard, addCard)
	split := container.NewHSplit(leftPanel, container.NewVScroll(rightPanel))
	split.SetOffset(0.35)

	w.SetContent(split)
	w.Show()
}

// buildIMAddCard creates the "Add Adapter" card.
func (a *App) buildIMAddCard(parent fyne.Window, cfg *config.Config, onAdded func()) *widget.Card {
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

	addBtn := widget.NewButtonWithIcon("Add Adapter", theme.ContentAddIcon(), func() {
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
		nameEntry.SetText("")
		platformSelect.SetSelected("")
		for _, e := range fieldEntries {
			e.SetText("")
		}
		statusLabel.SetText("Added: " + name)
		onAdded()
	})

	return widget.NewCard("Add Adapter", "", container.NewVBox(
		widget.NewForm(
			&widget.FormItem{Text: "Platform", Widget: platformSelect},
			&widget.FormItem{Text: "Name", Widget: nameEntry},
		),
		fieldsBox,
		container.NewHBox(addBtn, statusLabel),
	))
}

// imAdapterEntries returns sorted adapter entries grouped by binding status.
func (a *App) imAdapterEntries() []imAdapterEntry {
	cfg := a.cfg
	if cfg.IM.Adapters == nil {
		return nil
	}
	currentWS := ""
	if a.dc != nil {
		currentWS = a.dc.WorkDir
	}

	var current, other, unbound, disabled []imAdapterEntry
	for name, ac := range cfg.IM.Adapters {
		e := imAdapterEntry{
			Name:     name,
			Platform: ac.Platform,
			Enabled:  ac.Enabled,
		}
		// Check workspace binding from config targets or extra
		if ws, ok := ac.Extra["workspace"]; ok {
			if s, ok := ws.(string); ok {
				e.Workspace = s
			}
		}
		e.IsCurrent = e.Workspace == currentWS

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

func shortPath(p string) string {
	if len(p) > 30 {
		return "..." + p[len(p)-27:]
	}
	return p
}

// placeholder for image display (future QR code support)
func newQRImage(data []byte) fyne.CanvasObject {
	img := canvas.NewImageFromResource(nil)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(200, 200))
	return img
}

// blankLine returns a simple spacer.
func blankLine() fyne.CanvasObject {
	return layout.NewSpacer()
}
