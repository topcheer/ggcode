package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/util"

	"fyne.io/fyne/v2/layout"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
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

	hintLbl := widget.NewLabel(t("im.code_hint"))
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
	// Unregister this instance so other processes can become primary.
	if a.imInstanceDetect != nil {
		a.imInstanceDetect.Unregister()
		a.imInstanceDetect = nil
	}
}

// ─── WeChat QR Code Authentication ───

const wechatILinkBaseURL = "https://ilinkai.weixin.qq.com"

// wechatILinkRequest makes an HTTP request to the WeChat iLink API.
func wechatILinkRequest(ctx context.Context, method, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return util.ReadAll(resp.Body, util.ReadLimitGeneral)
}

// requestWechatQRCode fetches a QR code from iLink for WeChat onboard.
func requestWechatQRCode(ctx context.Context) (qrcodeToken string, imgPNG []byte, err error) {
	url := wechatILinkBaseURL + "/ilink/bot/get_bot_qrcode?bot_type=3"
	data, err := wechatILinkRequest(ctx, http.MethodGet, url)
	if err != nil {
		return "", nil, fmt.Errorf("QR code request failed: %w", err)
	}

	var resp struct {
		QRCode           string `json:"qrcode"`
		QRCodeImgContent string `json:"qrcode_img_content"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", nil, fmt.Errorf("QR code decode failed: %w", err)
	}
	if resp.QRCode == "" {
		return "", nil, fmt.Errorf("empty QR code token in response")
	}

	// qrcode_img_content is text content (URL) for generating QR image.
	png, err := qrcode.Encode(resp.QRCodeImgContent, qrcode.Medium, 256)
	if err != nil {
		return "", nil, fmt.Errorf("QR image render failed: %w", err)
	}
	return resp.QRCode, png, nil
}

// pollWechatQRStatus polls the QR code scan status.
func pollWechatQRStatus(ctx context.Context, qrcodeToken string) (status string, botToken string, err error) {
	url := wechatILinkBaseURL + "/ilink/bot/get_qrcode_status?qrcode=" + qrcodeToken
	data, err := wechatILinkRequest(ctx, http.MethodGet, url)
	if err != nil {
		return "", "", err
	}

	var resp struct {
		Status   string `json:"status"`
		BotToken string `json:"bot_token"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", fmt.Errorf("decode status: %w", err)
	}
	return resp.Status, resp.BotToken, nil
}

// showWechatQRAuthWindow opens a window displaying a QR code for WeChat scan-to-onboard.
func (a *App) showWechatQRAuthWindow(adapterName string) {
	if a.fyneApp == nil || a.window == nil {
		return
	}

	w := a.fyneApp.NewWindow("WeChat — Scan QR Code")
	w.Resize(fyne.NewSize(350, 420))

	statusLabel := widget.NewLabel(t("im.loading_qr"))
	qrImg := &canvas.Image{}
	qrImg.FillMode = canvas.ImageFillContain
	qrImg.SetMinSize(fyne.NewSize(256, 256))

	content := container.NewVBox(
		widget.NewLabelWithStyle("Scan this QR code with WeChat", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		qrImg,
		statusLabel,
	)
	w.SetContent(content)
	w.Show()

	// Fetch QR code in background
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		token, png, err := requestWechatQRCode(ctx)
		if err != nil {
			fyne.Do(func() { statusLabel.SetText(t("im.error", err.Error())) })
			return
		}

		// Display QR image
		// Set QR image from PNG bytes
		qrImg.Resource = fyne.NewStaticResource("qr.png", png)

		fyne.Do(func() {
			statusLabel.SetText(t("im.waiting_scan"))
			qrImg.Refresh()
			content.Refresh()
		})

		// Poll for scan status
		for i := 0; i < 60; i++ { // max 5 minutes
			time.Sleep(5 * time.Second)
			pollCtx, pollCancel := context.WithTimeout(context.Background(), 10*time.Second)
			status, botToken, err := pollWechatQRStatus(pollCtx, token)
			pollCancel()

			if err != nil {
				fyne.Do(func() { statusLabel.SetText(t("im.poll_error", err.Error())) })
				continue
			}

			switch status {
			case "confirmed":
				fyne.Do(func() {
					statusLabel.SetText(t("im.scan_confirmed"))
				})
				// Save bot_token to adapter config
				a.saveWechatBotToken(adapterName, botToken)
				fyne.Do(func() {
					w.Close()
					a.refreshIMWindow()
				})
				return
			case "scanned":
				fyne.Do(func() { statusLabel.SetText(t("im.scanned_confirming")) })
			default:
				// keep polling
			}
		}
		fyne.Do(func() { statusLabel.SetText(t("im.qr_expired")) })
	}()
}

// saveWechatBotToken saves the bot token to the adapter config and starts it.
// If adapterName already exists in config, it updates the bot_token in place.
// If adapterName is empty, it auto-generates a new name (wechat, wechat-2, ...).
func (a *App) saveWechatBotToken(adapterName, botToken string) {
	if a.cfg == nil {
		return
	}

	name := adapterName
	if name == "" {
		name = "wechat"
		n := 2
		for {
			if _, exists := a.cfg.IM.Adapters[name]; !exists {
				break
			}
			name = fmt.Sprintf("wechat-%d", n)
			n++
		}
	}

	// If adapter already exists, update bot_token in place
	if acfg, exists := a.cfg.IM.Adapters[name]; exists {
		if acfg.Extra == nil {
			acfg.Extra = make(map[string]interface{})
		}
		acfg.Extra["bot_token"] = botToken
		acfg.Enabled = true
		a.cfg.IM.Adapters[name] = acfg
		_ = a.cfg.Save()
	} else {
		// New adapter
		if err := a.cfg.AddIMAdapter(name, config.IMAdapterConfig{
			Enabled:  true,
			Platform: "wechat",
			Extra: map[string]interface{}{
				"bot_token": botToken,
			},
		}); err != nil {
			return
		}
		_ = a.cfg.Save()
	}

	// Start the adapter
	if a.imManager != nil {
		adapters := make(map[string]bool)
		for n, acfg := range a.cfg.IM.Adapters {
			adapters[n] = acfg.Enabled
		}
		a.imManager.ApplyAdapterConfig(adapters)
	}
}

// startWechatQRAuth handles the QR auth flow for a new WeChat adapter.
func (a *App) startWechatQRAuth(adapterName string) {
	a.showWechatQRAuthWindow(adapterName)
}

// showContactQRWindow opens a window displaying a QR code from an adapter's ContactURI.
// This is the generic QR display for platforms that expose a contact link after starting.
func (a *App) showContactQRWindow(adapterName string) {
	if a.fyneApp == nil || a.window == nil {
		return
	}

	// Find the adapter state
	var contactURI string
	if a.imManager != nil {
		for _, s := range a.imManager.Snapshot().Adapters {
			if s.Name == adapterName && s.ContactURI != "" {
				contactURI = s.ContactURI
				break
			}
		}
	}

	if contactURI == "" {
		dialog.ShowInformation("No QR Code", "Adapter has not generated a contact link yet. Make sure the adapter is enabled and running.", a.window)
		return
	}

	w := a.fyneApp.NewWindow(adapterName + " — Scan to Add")
	w.Resize(fyne.NewSize(350, 450))

	// Render QR code from contact URI
	png, err := qrcode.Encode(contactURI, qrcode.Medium, 256)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to render QR code: %w", err), a.window)
		return
	}

	qrImg := &canvas.Image{}
	qrImg.Resource = fyne.NewStaticResource("qr.png", png)
	qrImg.FillMode = canvas.ImageFillContain
	qrImg.SetMinSize(fyne.NewSize(256, 256))

	content := container.NewVBox(
		widget.NewLabelWithStyle("Scan to add this bot", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		qrImg,
		widget.NewLabel(contactURI),
		layout.NewSpacer(),
		widget.NewButton(t("im.copy_link"), func() {
			if a.fyneApp.Driver() != nil {
				a.window.Clipboard().SetContent(contactURI)
			}
		}),
	)
	w.SetContent(content)
	w.Show()
}
