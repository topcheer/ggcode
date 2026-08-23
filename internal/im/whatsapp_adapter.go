package im

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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

	imagepkg "github.com/topcheer/ggcode/internal/image"
)

func init() {
	// Configure device identity to appear as WhatsApp Web (Chrome browser).
	// The default PlatformType=UNKNOWN causes "unable to link device" errors from
	// WhatsApp servers during QR code pairing.
	store.SetOSInfo("ggcode", [3]uint32{2, 3000, 15})
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
}

const (
	waMaxTextLen    = 65536                  // Official: WhatsApp personal accounts support up to 65,536 characters per text message
	waInterMsgDelay = 300 * time.Millisecond // Conservative inter-chunk delay to avoid WhatsApp rate limiting
	waDedupMaxSize  = 1000                   // inbound msgid dedup cap, mirrors wecomDedupMaxSize (#974)
)

var waBackoffs = []time.Duration{
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	1 * time.Minute,
}

var errWhatsAppLoggedOut = errors.New("whatsapp logged out")

// waNextBackoff returns the reconnect delay for the given 0-based attempt,
// capped at the largest entry of waBackoffs (60s). Reconnect attempts are
// unbounded: a network outage — however long — must not permanently kill the
// adapter (#603). Only errWhatsAppLoggedOut is terminal (see waTerminal).
func waNextBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(waBackoffs) {
		return waBackoffs[len(waBackoffs)-1]
	}
	return waBackoffs[attempt]
}

// waTerminal reports whether a connectAndServe error is permanent. The only
// permanent case is errWhatsAppLoggedOut (device unpaired; requires manual QR
// re-pairing). Everything else — including multi-minute network outages —
// retries forever with capped backoff (#603).
func waTerminal(err error) bool {
	return errors.Is(err, errWhatsAppLoggedOut)
}

// ---------------------------------------------------------------------------
// Adapter struct
// ---------------------------------------------------------------------------

type whatsappAdapter struct {
	name    string
	manager *Manager

	client   *whatsmeow.Client
	storeDir string
	proxy    string

	mu        sync.RWMutex
	connected bool
	cancel    context.CancelFunc

	// runWG tracks the run goroutine(s) started by Start so shutdown paths
	// (and tests) can wait for them to finish writing session state (#603).
	runWG sync.WaitGroup

	// seen deduplicates inbound messages by msgid — reconnects and offline
	// history sync can redeliver the same message (#974). Guarded by a.mu.
	seen map[string]time.Time

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
		seen:     make(map[string]time.Time),
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
	client := a.currentClient()
	if client == nil || !a.Connected() {
		// #974: returning nil here reported success during disconnect windows
		// while the message was silently dropped.
		return fmt.Errorf("whatsapp %q: not connected, outbound dropped", a.name)
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
		if err := a.sendExtractedImage(ctx, client, jid, img); err != nil {
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
		_, err := client.SendMessage(ctx, jid, msg)
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
func (a *whatsappAdapter) sendExtractedImage(ctx context.Context, client *whatsmeow.Client, jid types.JID, img ExtractedImage) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			return a.sendImageByUpload(ctx, client, jid, data, "")
		}
		// Download remote URL
		req, err := http.NewRequestWithContext(ctx, "GET", img.Data, nil)
		if err != nil {
			return fmt.Errorf("create request for image URL: %w", err)
		}
		resp, err := imageDownloadClient.Do(req)
		if err != nil {
			return fmt.Errorf("download image: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("download image: HTTP %d", resp.StatusCode)
		}
		data, err := imagepkg.ReadLimited(resp.Body, imagepkg.MaxSize) // shared 20MB limit (#388)
		if err != nil {
			return fmt.Errorf("read image data: %w", err)
		}
		return a.sendImageByUpload(ctx, client, jid, data, "")
	case "data_url":
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return fmt.Errorf("invalid data URL")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return fmt.Errorf("invalid base64 in data URL: %w", err)
		}
		return a.sendImageByUpload(ctx, client, jid, data, "")
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// sendImageByUpload uploads image data to WhatsApp servers and sends as ImageMessage.
func (a *whatsappAdapter) sendImageByUpload(ctx context.Context, client *whatsmeow.Client, jid types.JID, data []byte, caption string) error {
	uploaded, err := client.Upload(ctx, data, whatsmeow.MediaImage)
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

	_, err = client.SendMessage(ctx, jid, &waE2E.Message{ImageMessage: imgMsg})
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
	a.mu.Lock()
	cancel := a.cancel
	client := a.client
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		client.Disconnect()
	}
}

// waitRunStopped blocks until every run goroutine started by Start has
// exited. Useful for deterministic shutdown in tests (#603).
func (a *whatsappAdapter) waitRunStopped() {
	a.runWG.Wait()
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
	client := a.currentClient()
	if client == nil {
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
	err = client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
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
	ctx, cancel := context.WithCancel(ctx)
	// Guard the cancel write with a.mu: Stop() reads a.cancel concurrently
	// (hot restart = stopAdapter -> Start), and an unsynchronized write/read
	// pair is a DATA RACE (#603).
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	a.runWG.Add(1)
	safego.Go("im.whatsapp.run", func() {
		defer a.runWG.Done()
		a.run(ctx)
	})
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

		if waTerminal(err) {
			debug.Log("whatsapp", "adapter %q: logged out, waiting for manual re-pair", a.name)
			return
		}
		if err == nil {
			// Clean disconnect, retry immediately
			attempt = 0
			debug.Log("whatsapp", "adapter %q: clean disconnect, reconnecting", a.name)
			continue
		}

		// Transient failures (network outages, server hiccups) retry
		// indefinitely with the backoff capped at the last waBackoffs entry —
		// previously 5 attempts (~108s) permanently killed the adapter on any
		// outage longer than ~2 minutes (#603).
		backoff := waNextBackoff(attempt)
		attempt++
		jittered := jitterDuration(backoff)
		debug.Log("whatsapp", "adapter %q: reconnect attempt %d in %v (jittered from %v)", a.name, attempt, jittered, backoff)
		a.publishState(false, "reconnecting", fmt.Sprintf("attempt %d in %v", attempt, jittered))

		select {
		case <-ctx.Done():
			return
		case <-time.After(jittered):
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
	// #974: the storeContainer/device struct fields were removed — the
	// container lives as a local here and is closed exactly once on return.
	// #761: every early return below (GetAllDevices / both Connect paths)
	// used to leak the sqlite pool; reconnectLoop retries indefinitely, so a
	// locked/Corrupt DB exhausted fds within hours. Defer-close once.
	containerClosed := false
	defer func() {
		if !containerClosed {
			_ = container.Close()
		}
	}()

	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		debug.Log("whatsapp", "adapter %q: get devices: %v", a.name, err)
		a.publishState(false, "error", fmt.Sprintf("devices: %v", err))
		return err
	}
	var device *store.Device
	if len(devices) > 0 {
		device = devices[0]
	} else {
		device = container.NewDevice()
	}

	client := whatsmeow.NewClient(device, &waDebugLogger{prefix: "client"})
	client.AddEventHandler(a.eventHandler())
	// Publish under a.mu: Stop() reads a.client concurrently and an
	// unsynchronized write/read pair is a DATA RACE (#603).
	a.mu.Lock()
	a.client = client
	a.mu.Unlock()
	done := make(chan error, 1)
	a.mu.Lock()
	a.sessionDone = done
	a.mu.Unlock()

	if client.Store.ID == nil {
		// No session — need QR login
		debug.Log("whatsapp", "adapter %q: no session, requesting QR code", a.name)
		a.publishState(false, "pairing", "scan QR code with WhatsApp")
		qrChan, qrErr := client.GetQRChannel(ctx)
		if qrErr != nil {
			debug.Log("whatsapp", "adapter %q: get QR channel: %v", a.name, qrErr)
		}
		if err := client.Connect(); err != nil {
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
				} else {
					// #974: don't silently swallow non-code QR channel events.
					debug.Log("whatsapp", "adapter %q: QR channel event %q", a.name, evt.Event)
				}
			}
		}
	} else {
		debug.Log("whatsapp", "adapter %q: connecting with saved session", a.name)
		if err := client.Connect(); err != nil {
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
	// Disconnect via snapshot — a.client may be nil'd concurrently by
	// markLoggedOut / Stop (#974 asymmetric-lock fix).
	teardown := func() {
		if c := a.currentClient(); c != nil {
			c.Disconnect()
		}
	}
	select {
	case <-ctx.Done():
		teardown()
		return nil
	case err := <-done:
		teardown()
		if err != nil && waTerminal(err) {
			// LoggedOut: close the sqlite container BEFORE deleting the DB
			// files — Windows refuses to unlink open files (#974).
			_ = container.Close()
			containerClosed = true
			a.removeStoreDB()
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
			// Snapshot under lock: Send/publishState run concurrently (#974).
			client := a.currentClient()
			jid := ""
			if client != nil && client.Store.ID != nil {
				jid = client.Store.ID.String()
			}
			debug.Log("whatsapp", "adapter %q: connected (jid=%s)", a.name, jid)
			a.publishState(true, "connected", "")

			if client != nil {
				if client.Store.PushName == "" {
					client.Store.PushName = "ggcode"
				}
				// Mark ourselves as available so the server starts pushing messages.
				if err := client.SendPresence(context.Background(), types.PresenceAvailable); err != nil {
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
						if err := client.FetchAppState(ctx, name, false, false); err != nil {
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
			debug.Log("whatsapp", "adapter %q: logged out: %s", a.name, v.Reason)
			a.markLoggedOut()
			a.publishState(false, "logged_out", "need re-pairing")
			a.signalSessionDone(errWhatsAppLoggedOut)
			// DB file removal happens in connectAndServe after the container is
			// closed — deleting an open sqlite file fails on Windows (#974).

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
		// Buffer full: upgrade a non-terminal signal to a terminal one so a
		// preceding Disconnected cannot mask LoggedOut (#974) — event handlers
		// run sequentially on whatsmeow's dispatch goroutine, so this is safe.
		if waTerminal(err) {
			select {
			case <-done:
			default:
			}
			select {
			case done <- err:
			default:
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Inbound
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) handleInbound(msg *events.Message) {
	// #974 (high): whatsmeow's event layer does NOT filter our own outgoing
	// messages — multi-device SyncMessage echoes arrive with IsFromMe=true.
	// Without this check a bound master's own messages re-enter the agent as
	// user input, causing ghost self-replies. Standard bridge/bot behavior is
	// to drop them.
	if msg.Info.IsFromMe {
		return
	}
	// #974: reconnects and offline history sync can redeliver the same msgid;
	// dedup like signal/slack/wecom do.
	a.mu.Lock()
	if _, dup := a.seen[msg.Info.ID]; dup {
		a.mu.Unlock()
		return
	}
	a.seen[msg.Info.ID] = time.Now()
	if len(a.seen) > waDedupMaxSize {
		cutoff := time.Now().Add(-5 * time.Minute)
		for k, t := range a.seen {
			if t.Before(cutoff) {
				delete(a.seen, k)
			}
		}
	}
	a.mu.Unlock()

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
	if text == "" {
		return nil
	}
	client := a.currentClient()
	if client == nil {
		return fmt.Errorf("whatsapp %q: not connected", a.name)
	}
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return err
	}
	_, err = client.SendMessage(context.Background(), jid, &waE2E.Message{
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
	if client := a.currentClient(); client != nil && client.Store.ID != nil {
		// JID.User is the phone number (e.g. "8613800138000")
		// wa.me deep link: https://wa.me/{phone}
		contactURI = "https://wa.me/" + client.Store.ID.User
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

// currentClient returns a lock-protected snapshot of the client pointer.
// The pointer itself is always safe to dereference: publish (connectAndServe)
// and clear (markLoggedOut) writes happen under a.mu, so every read site must
// go through this snapshot instead of touching a.client directly (#974).
func (a *whatsappAdapter) currentClient() *whatsmeow.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client
}

// markLoggedOut clears the client and connected flag (idempotent). Called
// from the LoggedOut event handler; safe to call again from other paths.
func (a *whatsappAdapter) markLoggedOut() {
	a.mu.Lock()
	client := a.client
	a.client = nil
	a.connected = false
	a.mu.Unlock()
	if client != nil {
		client.Disconnect()
	}
}

// removeStoreDB deletes the whatsmeow sqlite store so the next start pairs
// fresh. Call only AFTER the container is closed — Windows refuses to unlink
// open files (#974).
func (a *whatsappAdapter) removeStoreDB() {
	dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	debug.Log("whatsapp", "adapter %q: store removed, will re-pair on next start", a.name)
}

// chunkWARunes delegates to the shared splitMessageRunes with balanced
// break preference, which searches the entire chunk for newline boundaries
// (wider than the previous 200-rune lookback) while still avoiding splits
// at very early newlines.
func chunkWARunes(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, true)
}
