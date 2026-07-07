package im

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/skip2/go-qrcode"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO required)

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

func init() {
	// Configure device identity to appear as WhatsApp Web (Chrome browser).
	// The default PlatformType=UNKNOWN causes "unable to link device" errors from
	// WhatsApp servers during QR code pairing.
	store.SetOSInfo("ggcode", [3]uint32{2, 3000, 15})
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
}

const (
	waMaxTextLen    = 4096
	waMaxReconnect  = 5
	waInterMsgDelay = 300 * time.Millisecond // Conservative inter-chunk delay to avoid WhatsApp rate limiting
)

var waBackoffs = []time.Duration{
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	1 * time.Minute,
}

var errWhatsAppLoggedOut = errors.New("whatsapp logged out")

// ---------------------------------------------------------------------------
// Adapter struct
// ---------------------------------------------------------------------------

type whatsappAdapter struct {
	name    string
	manager *Manager

	storeContainer *sqlstore.Container
	client         *whatsmeow.Client
	device         *store.Device
	storeDir       string
	proxy          string

	mu        sync.RWMutex
	connected bool
	cancel    context.CancelFunc

	// QR code for TUI display (set during pairing, cleared after connect)
	lastQR      string
	sessionDone chan error
}

func newWhatsAppAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*whatsappAdapter, error) {
	homeDir := config.HomeDir()
	storeDir := stringValue(adapterCfg.Extra, "store_dir")
	if storeDir == "" {
		storeDir = filepath.Join(homeDir, ".ggcode", "credentials", "whatsapp", name)
	}
	if err := os.MkdirAll(storeDir, 0700); err != nil {
		return nil, fmt.Errorf("whatsapp %q: create store dir: %w", name, err)
	}

	proxy := resolveProxy(stringValue(adapterCfg.Extra, "proxy"), "WHATSAPP_PROXY")

	return &whatsappAdapter{
		name:     name,
		manager:  mgr,
		storeDir: storeDir,
		proxy:    proxy,
	}, nil
}

// ---------------------------------------------------------------------------
// Sink interface
// ---------------------------------------------------------------------------

// waDebugLogger bridges whatsmeow's internal logger to our debug log system.
type waDebugLogger struct{ prefix string }

func (l *waDebugLogger) Debugf(msg string, args ...interface{}) {
	debug.Log("whatsapp", l.prefix+": "+msg, args...)
}
func (l *waDebugLogger) Infof(msg string, args ...interface{}) {
	debug.Log("whatsapp", l.prefix+": "+msg, args...)
}
func (l *waDebugLogger) Warnf(msg string, args ...interface{}) {
	debug.Log("whatsapp", l.prefix+": "+msg, args...)
}
func (l *waDebugLogger) Errorf(msg string, args ...interface{}) {
	debug.Log("whatsapp", l.prefix+": "+msg, args...)
}
func (l *waDebugLogger) Sub(module string) waLog.Logger {
	return &waDebugLogger{prefix: l.prefix + "/" + module}
}

func (a *whatsappAdapter) Name() string { return a.name }

func (a *whatsappAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	if a.client == nil || !a.Connected() {
		return nil
	}
	content := defaultOutboundText(event)
	if content == "" {
		return nil
	}

	target := binding.ChannelID
	if target == "" {
		target = binding.TargetID
	}
	if target == "" {
		return nil
	}

	jid, err := types.ParseJID(target)
	if err != nil {
		return fmt.Errorf("whatsapp %q: parse JID %q: %w", a.name, target, err)
	}

	// Extract images from text and send them as WhatsApp image messages.
	images, remainingText := ExtractImagesFromText(content)
	for i, img := range images {
		if err := a.sendExtractedImage(ctx, jid, img); err != nil {
			debug.Log("whatsapp", "adapter %q: image send failed [%d/%d]: %v", a.name, i+1, len(images), err)
		}
	}

	// Send remaining text
	text := markdownToWhatsApp(remainingText)
	if text == "" {
		debug.Log("whatsapp", "adapter %q: outbound target=%s images=%d (text empty after extraction)", a.name, target, len(images))
		return nil
	}

	chunks := chunkWARunes(text, waMaxTextLen)
	debug.Log("whatsapp", "adapter %q: outbound target=%s chunks=%d images=%d len=%d", a.name, target, len(chunks), len(images), len(text))
	for i, chunk := range chunks {
		msg := &waE2E.Message{Conversation: proto.String(chunk)}
		_, err := a.client.SendMessage(ctx, jid, msg)
		if err != nil {
			debug.Log("whatsapp", "adapter %q: send chunk %d/%d failed: %v", a.name, i+1, len(chunks), err)
			return fmt.Errorf("whatsapp %q: send chunk %d: %w", a.name, i+1, err)
		}
		if i < len(chunks)-1 {
			select {
			case <-time.After(waInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	debug.Log("whatsapp", "adapter %q: outbound delivered target=%s chunks=%d images=%d", a.name, target, len(chunks), len(images))
	return nil
}

// sendExtractedImage dispatches image sending based on the image kind.
func (a *whatsappAdapter) sendExtractedImage(ctx context.Context, jid types.JID, img ExtractedImage) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			return a.sendImageByUpload(ctx, jid, data, "")
		}
		// Download remote URL
		req, err := http.NewRequestWithContext(ctx, "GET", img.Data, nil)
		if err != nil {
			return fmt.Errorf("create request for image URL: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("download image: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("download image: HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // 20MB max
		if err != nil {
			return fmt.Errorf("read image data: %w", err)
		}
		return a.sendImageByUpload(ctx, jid, data, "")
	case "data_url":
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return fmt.Errorf("invalid data URL")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return fmt.Errorf("invalid base64 in data URL: %w", err)
		}
		return a.sendImageByUpload(ctx, jid, data, "")
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// sendImageByUpload uploads image data to WhatsApp servers and sends as ImageMessage.
func (a *whatsappAdapter) sendImageByUpload(ctx context.Context, jid types.JID, data []byte, caption string) error {
	uploaded, err := a.client.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("whatsapp upload image: %w", err)
	}

	imgMsg := &waE2E.ImageMessage{
		Mimetype:      proto.String(http.DetectContentType(data)),
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uint64(len(data))),
	}
	if caption != "" {
		imgMsg.Caption = proto.String(caption)
	}

	_, err = a.client.SendMessage(ctx, jid, &waE2E.Message{ImageMessage: imgMsg})
	if err != nil {
		return fmt.Errorf("whatsapp send image: %w", err)
	}
	debug.Log("whatsapp", "adapter %q: image sent to=%s bytes=%d", a.name, jid.String(), len(data))
	return nil
}

func (a *whatsappAdapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

func (a *whatsappAdapter) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.client != nil {
		a.client.Disconnect()
	}
}

// Close implements the Closer interface so the runtime can properly
// shut down the adapter. Without this, the runtime logs a warning and
// leaks the WhatsApp websocket connection.
func (a *whatsappAdapter) Close() error {
	a.Stop()
	return nil
}

func (a *whatsappAdapter) ChatID() string { return "" }

// ---------------------------------------------------------------------------
// Typing indicator
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	if a.client == nil {
		return nil
	}
	target := binding.ChannelID
	if target == "" {
		target = binding.TargetID
	}
	jid, err := types.ParseJID(target)
	if err != nil {
		return err
	}
	err = a.client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	if err != nil {
		debug.Log("whatsapp", "adapter %q: typing failed: %v", a.name, err)
	}
	return err
}

func (a *whatsappAdapter) SupportsTyping() bool { return true }

// ---------------------------------------------------------------------------
// Start / connection lifecycle
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) Start(ctx context.Context) {
	debug.Log("whatsapp", "adapter %q start", a.name)
	ctx, a.cancel = context.WithCancel(ctx)
	safego.Go("im.whatsapp.run", func() { a.run(ctx) })
}

func (a *whatsappAdapter) run(ctx context.Context) {
	// Reconnect loop with exponential backoff
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := a.connectAndServe(ctx)

		select {
		case <-ctx.Done():
			return
		default:
		}

		if errors.Is(err, errWhatsAppLoggedOut) {
			debug.Log("whatsapp", "adapter %q: logged out, waiting for manual re-pair", a.name)
			return
		}
		if err == nil {
			// Clean disconnect, retry immediately
			attempt = 0
			debug.Log("whatsapp", "adapter %q: clean disconnect, reconnecting", a.name)
			continue
		}

		if attempt >= waMaxReconnect {
			debug.Log("whatsapp", "adapter %q: max reconnect attempts reached", a.name)
			a.publishState(false, "error", "max reconnect attempts reached")
			return
		}

		backoff := waBackoffs[attempt]
		if attempt >= len(waBackoffs) {
			backoff = waBackoffs[len(waBackoffs)-1]
		}
		attempt++
		debug.Log("whatsapp", "adapter %q: reconnect attempt %d in %v", a.name, attempt, backoff)
		a.publishState(false, "reconnecting", fmt.Sprintf("attempt %d in %v", attempt, backoff))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// connectAndServe handles a single connection lifecycle.
// On failure or logout, the caller (reconnectLoop) retries.
func (a *whatsappAdapter) connectAndServe(ctx context.Context) error {
	dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
	container, err := sqlstore.New(ctx, "sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath), &waDebugLogger{prefix: "store"})
	if err != nil {
		debug.Log("whatsapp", "adapter %q: open store: %v", a.name, err)
		a.publishState(false, "error", fmt.Sprintf("store: %v", err))
		return err
	}
	a.storeContainer = container

	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		debug.Log("whatsapp", "adapter %q: get devices: %v", a.name, err)
		a.publishState(false, "error", fmt.Sprintf("devices: %v", err))
		return err
	}
	if len(devices) > 0 {
		a.device = devices[0]
	} else {
		a.device = container.NewDevice()
	}

	a.client = whatsmeow.NewClient(a.device, &waDebugLogger{prefix: "client"})
	a.client.AddEventHandler(a.eventHandler())
	done := make(chan error, 1)
	a.mu.Lock()
	a.sessionDone = done
	a.mu.Unlock()

	if a.client.Store.ID == nil {
		// No session — need QR login
		debug.Log("whatsapp", "adapter %q: no session, requesting QR code", a.name)
		a.publishState(false, "pairing", "scan QR code with WhatsApp")
		qrChan, _ := a.client.GetQRChannel(ctx)
		if err := a.client.Connect(); err != nil {
			debug.Log("whatsapp", "adapter %q: connect: %v", a.name, err)
			return err
		}
		if qrChan != nil {
			for evt := range qrChan {
				if evt.Event == "code" {
					debug.Log("whatsapp", "adapter %q: QR code generated", a.name)
					img, _ := qrcode.New(evt.Code, qrcode.Medium)
					img.DisableBorder = false
					qrASCII := strings.TrimRight(img.ToSmallString(false), "\n")
					a.mu.Lock()
					a.lastQR = qrASCII
					a.mu.Unlock()
					// Publish state with QR code so TUI can display it
					a.publishState(false, "pairing", "scan QR code with WhatsApp")
				}
			}
		}
	} else {
		debug.Log("whatsapp", "adapter %q: connecting with saved session", a.name)
		if err := a.client.Connect(); err != nil {
			debug.Log("whatsapp", "adapter %q: connect: %v", a.name, err)
			return err
		}
	}

	defer func() {
		a.mu.Lock()
		if a.sessionDone == done {
			a.sessionDone = nil
		}
		a.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		if a.client != nil {
			a.client.Disconnect()
		}
		if a.storeContainer != nil {
			_ = a.storeContainer.Close()
		}
		return nil
	case err := <-done:
		if a.client != nil {
			a.client.Disconnect()
		}
		if a.storeContainer != nil {
			_ = a.storeContainer.Close()
		}
		return err
	}
}

// ---------------------------------------------------------------------------
// Event handler
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) eventHandler() func(interface{}) {
	return func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Connected:
			a.mu.Lock()
			a.connected = true
			a.lastQR = "" // clear QR after successful connect
			a.mu.Unlock()
			jid := ""
			if a.client != nil && a.client.Store.ID != nil {
				jid = a.client.Store.ID.String()
			}
			debug.Log("whatsapp", "adapter %q: connected (jid=%s)", a.name, jid)
			a.publishState(true, "connected", "")

			if a.client != nil {
				if a.client.Store.PushName == "" {
					a.client.Store.PushName = "ggcode"
				}
				// Mark ourselves as available so the server starts pushing messages.
				if err := a.client.SendPresence(context.Background(), types.PresenceAvailable); err != nil {
					debug.Log("whatsapp", "adapter %q: send presence available failed: %v", a.name, err)
				} else {
					debug.Log("whatsapp", "adapter %q: presence set to available", a.name)
				}

				// Fetch critical app state (encryption keys, contact list, group metadata).
				// Without these, the client cannot decrypt incoming messages.
				// Matches mautrix-whatsapp bridge's post-connect initialization.
				safego.Go("im.whatsapp.appstate", func() {
					ctx := context.Background()
					for _, name := range []appstate.WAPatchName{
						appstate.WAPatchCriticalBlock,
						appstate.WAPatchCriticalUnblockLow,
					} {
						if err := a.client.FetchAppState(ctx, name, false, false); err != nil {
							debug.Log("whatsapp", "adapter %q: fetch app state %s failed: %v", a.name, name, err)
						} else {
							debug.Log("whatsapp", "adapter %q: fetched app state %s", a.name, name)
						}
					}
				})
			}

		case *events.Disconnected:
			a.mu.Lock()
			a.connected = false
			a.mu.Unlock()
			debug.Log("whatsapp", "adapter %q: disconnected", a.name)
			a.publishState(false, "disconnected", "")
			a.signalSessionDone(fmt.Errorf("whatsapp disconnected"))

		case *events.LoggedOut:
			a.mu.Lock()
			a.connected = false
			a.mu.Unlock()
			debug.Log("whatsapp", "adapter %q: logged out: %s", a.name, v.Reason)
			// Clear device reference so next start creates fresh device
			a.device = nil
			// Remove the database so next start generates a new QR code
			dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
			_ = os.Remove(dbPath)
			_ = os.Remove(dbPath + "-wal")
			_ = os.Remove(dbPath + "-shm")
			debug.Log("whatsapp", "adapter %q: device cleared, will re-pair on next start", a.name)
			a.publishState(false, "logged_out", "need re-pairing")
			a.signalSessionDone(errWhatsAppLoggedOut)

		case *events.PairSuccess:
			debug.Log("whatsapp", "adapter %q: paired (JID: %s)", a.name, v.ID)

		case *events.Message:
			a.handleInbound(v)

		case *events.HistorySync:
			debug.Log("whatsapp", "adapter %q: history sync (progress=%d, items=%d)", a.name, v.Data.GetProgress(), len(v.Data.GetConversations()))

		case *events.OfflineSyncPreview:
			debug.Log("whatsapp", "adapter %q: offline sync preview (total=%d, messages=%d)", a.name, v.Total, v.Messages)

		case *events.OfflineSyncCompleted:
			debug.Log("whatsapp", "adapter %q: offline sync completed (count=%d)", a.name, v.Count)

		case *events.JoinedGroup:
			debug.Log("whatsapp", "adapter %q: joined group (JID=%s)", a.name, v.JID)

		case *events.GroupInfo:
			debug.Log("whatsapp", "adapter %q: group info update (JID=%s)", a.name, v.JID)

		case *events.PairError:
			debug.Log("whatsapp", "adapter %q: pair error: %v", a.name, v)

		default:
			// Log all unhandled events so we can diagnose missing handlers
			debug.Log("whatsapp", "adapter %q: unhandled event %T", a.name, evt)
		}
	}
}

func (a *whatsappAdapter) signalSessionDone(err error) {
	a.mu.RLock()
	done := a.sessionDone
	a.mu.RUnlock()
	if done == nil {
		return
	}
	select {
	case done <- err:
	default:
	}
}

// ---------------------------------------------------------------------------
// Inbound
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) handleInbound(msg *events.Message) {
	text := ""
	if conv := msg.Message.GetConversation(); conv != "" {
		text = conv
	} else if ext := msg.Message.GetExtendedTextMessage(); ext != nil {
		text = ext.GetText()
	}
	text = strings.TrimSpace(text)

	debug.Log("whatsapp", "adapter %q: inbound message from=%s chat=%s isFromMe=%v text=%q", a.name, msg.Info.Sender, msg.Info.Chat, msg.Info.IsFromMe, text)

	if text == "" {
		return
	}

	sender := msg.Info.Sender.String()
	chatID := msg.Info.Chat.String()

	// After pairing, only accept messages from the bound channel.
	// Messages from other groups/chats are silently dropped.
	if a.manager != nil {
		snap := a.manager.Snapshot()
		if binding := snap.BindingByAdapter(a.name); binding != nil {
			if binding.ChannelID != "" && binding.ChannelID != chatID {
				debug.Log("whatsapp", "adapter %q: dropping message from unbound channel %q (bound=%q)", a.name, chatID, binding.ChannelID)
				return
			}
		}
	}
	debug.Log("whatsapp", "adapter %q: inbound chat=%s sender=%s len=%d", a.name, chatID, sender, len(text))

	waMsg := InboundMessage{
		Text: text,
		Envelope: Envelope{
			Platform:  PlatformWhatsApp,
			Adapter:   a.name,
			ChannelID: chatID,
			SenderID:  sender,
		},
	}

	// Pairing flow first
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(waMsg)
		if err != nil && err != ErrNoSessionBound {
			debug.Log("whatsapp", "adapter %q: pairing: %v", a.name, err)
		}
		if pairingResult.Consumed {
			_ = a.replyToChat(chatID, pairingResult.ReplyText)
			if err := a.manager.NotifyPreviousBindingReplaced(context.Background(), pairingResult); err != nil {
				debug.Log("whatsapp", "adapter %q: notify previous: %v", a.name, err)
			}
			return
		}
	}

	// Normal inbound
	if a.manager != nil {
		safego.Go(fmt.Sprintf("whatsapp-inbound-%s", a.name), func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			a.manager.HandleInbound(ctx, waMsg)
		})
	}
}

func (a *whatsappAdapter) replyToChat(chatID, text string) error {
	if a.client == nil || text == "" {
		return nil
	}
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return err
	}
	_, err = a.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String(text),
	})
	if err != nil {
		debug.Log("whatsapp", "adapter %q: reply to %s failed: %v", a.name, chatID, err)
	} else {
		debug.Log("whatsapp", "adapter %q: reply sent to %s len=%d", a.name, chatID, len(text))
	}
	return err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	contactURI := ""
	if a.client != nil && a.client.Store.ID != nil {
		// JID.User is the phone number (e.g. "8613800138000")
		// wa.me deep link: https://wa.me/{phone}
		contactURI = "https://wa.me/" + a.client.Store.ID.User
	}
	a.mu.RLock()
	qr := a.lastQR
	a.mu.RUnlock()

	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformWhatsApp,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		QRCode:     qr,
		UpdatedAt:  time.Now(),
	})
}

// chunkWARunes delegates to the shared splitMessageRunes with balanced
// break preference, which searches the entire chunk for newline boundaries
// (wider than the previous 200-rune lookback) while still avoiding splits
// at very early newlines.
func chunkWARunes(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, true)
}
