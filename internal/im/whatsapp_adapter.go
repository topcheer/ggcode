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
	_ "modernc.org/sqlite"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"

	imagepkg "github.com/topcheer/ggcode/internal/image"
)

func init() {

	store.SetOSInfo("ggcode", [3]uint32{2, 3000, 15})
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
}

const (
	waMaxTextLen    = 65536
	waInterMsgDelay = 300 * time.Millisecond
	waDedupMaxSize  = 1000
)

var waBackoffs = []time.Duration{
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	1 * time.Minute,
}

var errWhatsAppLoggedOut = errors.New("whatsapp logged out")

func waNextBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(waBackoffs) {
		return waBackoffs[len(waBackoffs)-1]
	}
	return waBackoffs[attempt]
}

func waTerminal(err error) bool {
	return errors.Is(err, errWhatsAppLoggedOut)
}

type whatsappAdapter struct {
	name    string
	manager *Manager

	client   *whatsmeow.Client
	storeDir string
	proxy    string

	mu        sync.RWMutex
	connected bool
	cancel    context.CancelFunc

	runWG sync.WaitGroup

	seen map[string]time.Time

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

	images, remainingText := ExtractImagesFromText(content)
	sentImage := false
	failedImages := 0
	for i, img := range images {
		if err := a.sendExtractedImage(ctx, client, jid, img); err != nil {
			failedImages++
			debug.Log("whatsapp", "adapter %q: image send failed [%d/%d]: %v", a.name, i+1, len(images), err)
			continue
		}
		sentImage = true

		select {
		case <-time.After(waInterMsgDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	text := markdownToWhatsApp(remainingText)
	if text == "" {
		if failedImages > 0 && !sentImage && len(images) > 0 {

			return fmt.Errorf("whatsapp %q: all %d image(s) failed to send", a.name, len(images))
		}
		debug.Log("whatsapp", "adapter %q: outbound target=%s images=%d (text empty after extraction)", a.name, target, len(images))
		return nil
	}

	chunks := chunkWARunes(text, waMaxTextLen)
	debug.Log("whatsapp", "adapter %q: outbound target=%s chunks=%d images=%d len=%d", a.name, target, len(chunks), len(images), len(text))
	for i, chunk := range chunks {
		if i > 0 || sentImage {

			select {
			case <-time.After(waInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		msg := &waE2E.Message{Conversation: proto.String(chunk)}
		_, err := client.SendMessage(ctx, jid, msg)
		if err != nil {
			debug.Log("whatsapp", "adapter %q: send chunk %d/%d failed: %v", a.name, i+1, len(chunks), err)
			return fmt.Errorf("whatsapp %q: send chunk %d: %w", a.name, i+1, err)
		}
	}
	debug.Log("whatsapp", "adapter %q: outbound delivered target=%s chunks=%d images=%d", a.name, target, len(chunks), len(images))
	return nil
}

func (a *whatsappAdapter) sendExtractedImage(ctx context.Context, client *whatsmeow.Client, jid types.JID, img ExtractedImage) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {

			f, err := os.Open(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			data, err := imagepkg.ReadLimited(f, imagepkg.MaxSize)
			f.Close()
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			return a.sendImageByUpload(ctx, client, jid, data, "")
		}

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
		data, err := imagepkg.ReadLimited(resp.Body, imagepkg.MaxSize)
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

func (a *whatsappAdapter) waitRunStopped() {
	a.runWG.Wait()
}

func (a *whatsappAdapter) Close() error {
	a.Stop()
	return nil
}

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

func (a *whatsappAdapter) Start(ctx context.Context) {
	debug.Log("whatsapp", "adapter %q start", a.name)
	ctx, cancel := context.WithCancel(ctx)

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

			attempt = 0
			debug.Log("whatsapp", "adapter %q: clean disconnect, reconnecting", a.name)
			continue
		}

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

func (a *whatsappAdapter) connectAndServe(ctx context.Context) error {
	dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
	container, err := sqlstore.New(ctx, "sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath), &waDebugLogger{prefix: "store"})
	if err != nil {
		debug.Log("whatsapp", "adapter %q: open store: %v", a.name, err)
		a.publishState(false, "error", fmt.Sprintf("store: %v", err))
		return err
	}

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

	a.mu.Lock()
	a.client = client
	a.mu.Unlock()
	done := make(chan error, 1)
	a.mu.Lock()
	a.sessionDone = done
	a.mu.Unlock()

	if client.Store.ID == nil {

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

					a.publishState(false, "pairing", "scan QR code with WhatsApp")
				} else {

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

			_ = container.Close()
			containerClosed = true
			a.removeStoreDB()
		}
		return err
	}
}

func (a *whatsappAdapter) eventHandler() func(interface{}) {
	return func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Connected:
			a.mu.Lock()
			a.connected = true
			a.lastQR = ""
			a.mu.Unlock()

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

				if err := client.SendPresence(context.Background(), types.PresenceAvailable); err != nil {
					debug.Log("whatsapp", "adapter %q: send presence available failed: %v", a.name, err)
				} else {
					debug.Log("whatsapp", "adapter %q: presence set to available", a.name)
				}

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

func whatsappMediaCaption(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if img := m.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}
	return ""
}

func (a *whatsappAdapter) handleInbound(msg *events.Message) {

	if msg.Info.IsFromMe {
		return
	}

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

		for len(a.seen) >= waDedupMaxSize {
			var oldestKey string
			var oldestT time.Time
			for k, t := range a.seen {
				if oldestKey == "" || t.Before(oldestT) {
					oldestKey, oldestT = k, t
				}
			}
			delete(a.seen, oldestKey)
		}
	}
	a.mu.Unlock()

	text := ""
	if conv := msg.Message.GetConversation(); conv != "" {
		text = conv
	} else if ext := msg.Message.GetExtendedTextMessage(); ext != nil {
		text = ext.GetText()
	}

	if strings.TrimSpace(text) == "" {
		text = whatsappMediaCaption(msg.Message)
	}
	text = strings.TrimSpace(text)

	debug.Log("whatsapp", "adapter %q: inbound message from=%s chat=%s isFromMe=%v text=%q", a.name, msg.Info.Sender, msg.Info.Chat, msg.Info.IsFromMe, text)

	if text == "" {
		return
	}

	sender := msg.Info.Sender.String()
	chatID := msg.Info.Chat.String()

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

func (a *whatsappAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	contactURI := ""
	if client := a.currentClient(); client != nil && client.Store.ID != nil {

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

func (a *whatsappAdapter) currentClient() *whatsmeow.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client
}

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

func (a *whatsappAdapter) removeStoreDB() {
	dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	debug.Log("whatsapp", "adapter %q: store removed, will re-pair on next start", a.name)
}

func chunkWARunes(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, true)
}
