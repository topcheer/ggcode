package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

func (a *App) showMetricsWindow() {
	if a.metricsWindow != nil {
		a.metricsWindow.RequestFocus()
		a.metricsWindow.Show()
		return
	}

	w := a.fyneApp.NewWindow(t("metrics.window_title"))
	setWindowIcon(w)
	sidebar := NewSidebar(a, a.agentBridge, a.ui)
	content := container.NewVScroll(container.NewVBox(
		sidebar.buildSessionMetricsCard(),
		sidebar.buildSessionMetricTurnsCard(),
	))
	w.SetContent(content)
	w.Resize(fyne.NewSize(560, 520))
	w.SetOnClosed(func() {
		a.metricsWindow = nil
	})
	a.metricsWindow = w
	w.Show()
}
