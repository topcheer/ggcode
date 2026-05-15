package main

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2/layout"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"fyne.io/fyne/v2/dialog"
	"github.com/topcheer/ggcode/internal/im"
)

// desktopIMBridge implements im.Bridge, routing inbound IM messages
// into the desktop agent for processing.
type desktopIMBridge struct {
	app *App
}

func newDesktopIMBridge(app *App) *desktopIMBridge {
	return &desktopIMBridge{app: app}
}

// SubmitInboundMessage is called by im.Manager when an IM message arrives.
// It injects the message as a user prompt into the active agent session.
func (b *desktopIMBridge) SubmitInboundMessage(ctx context.Context, msg im.InboundMessage) error {
	if b.app == nil {
		return fmt.Errorf("app not available")
	}

	// Build the text from the inbound message.
	text := buildInboundText(msg)
	if text == "" {
		return nil
	}

	// Run on the UI goroutine to safely interact with agent.
	fyne.Do(func() {
		if b.app.agentBridge != nil {
			b.app.agentBridge.Send(text)
		}
	})

	return nil
}

// buildInboundText extracts displayable text from an inbound message.
func buildInboundText(msg im.InboundMessage) string {
	blocks := msg.ProviderContent()
	if len(blocks) == 0 {
		return strings.TrimSpace(msg.Text)
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return strings.TrimSpace(msg.Text)
}

// showPairingChallenge displays a dialog for the user to confirm
// a pairing challenge from an IM adapter.
func (a *App) showPairingChallenge(challenge im.PairingChallenge) {
	if a.window == nil {
		return
	}

	adapterName := challenge.Adapter
	code := challenge.Code

	msg := fmt.Sprintf("IM adapter '%s' is requesting to pair.\n\nEnter this code in your IM channel:\n\n    %s", adapterName, code)

	fyne.Do(func() {
		dialog.ShowInformation("IM Pairing Request", msg, a.window)
	})
}

// showPairingCode displays the pairing code for platforms that need scan+confirm.
func showPairingCode(w fyne.Window, adapterName, code string) {
	msg := fmt.Sprintf("Adapter '%s' pairing code:\n\n    %s\n\nEnter this code in the IM channel to complete binding.", adapterName, code)
	dialog.ShowInformation("Pairing Code", msg, w)
}

// startIMAdapters starts all enabled adapters bound to the current workspace.
func (a *App) startIMAdapters() {
	if a.imManager == nil || a.cfg == nil || !a.cfg.IM.Enabled {
		return
	}

	// Set bridge so inbound messages reach our agent.
	a.imManager.SetBridge(newDesktopIMBridge(a))

	// Start adapters bound to current workspace.
	controller, err := im.StartCurrentBindingAdapter(context.Background(), a.cfg.IM, a.imManager)
	if err != nil {
		fmt.Printf("IM adapter start error: %v\n", err)
		return
	}
	a.imController = controller
}

// showPairingCodeDialog opens a window with a large pairing code display.
func (a *App) showPairingCodeDialog(ch *im.PairingChallenge) {
	if a.fyneApp == nil {
		return
	}
	w := a.fyneApp.NewWindow("IM Pairing Request")
	w.Resize(fyne.NewSize(400, 300))
	w.SetOnClosed(func() { a.imPairingWin = nil })
	a.imPairingWin = w

	adapterLbl := widget.NewLabel(fmt.Sprintf("%s (%s)", ch.Adapter, ch.Platform))
	adapterLbl.Alignment = fyne.TextAlignCenter

	hintLbl := widget.NewLabel("Enter this code in your IM channel:")
	hintLbl.Alignment = fyne.TextAlignCenter

	codeText := canvas.NewText(ch.Code, theme.ForegroundColor())
	codeText.TextSize = 48
	codeText.TextStyle = fyne.TextStyle{Bold: true}
	codeText.Alignment = fyne.TextAlignCenter

	w.SetContent(container.NewVBox(
		layout.NewSpacer(),
		adapterLbl,
		hintLbl,
		codeText,
		layout.NewSpacer(),
	))
	w.Show()
}

// stopIMAdapters stops all running IM adapters.
func (a *App) stopIMAdapters() {
	if a.imController != nil {
		a.imController.Stop()
		a.imController = nil
	}
}
