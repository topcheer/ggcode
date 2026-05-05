package im

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/skip2/go-qrcode"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	waMaxTextLen = 4096
)

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
}

func newWhatsAppAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*whatsappAdapter, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("whatsapp %q: resolve home: %w", name, err)
	}
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

func (a *whatsappAdapter) Name() string { return a.name }

func (a *whatsappAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	if a.client == nil || !a.Connected() {
		return nil
	}
	if event.Kind != OutboundEventText || event.Text == "" {
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

	chunks := chunkWAText(event.Text, waMaxTextLen)
	for i, chunk := range chunks {
		msg := &waE2E.Message{Conversation: proto.String(chunk)}
		_, err := a.client.SendMessage(ctx, jid, msg)
		if err != nil {
			return fmt.Errorf("whatsapp %q: send chunk %d: %w", a.name, i+1, err)
		}
		if i < len(chunks)-1 {
			time.Sleep(300 * time.Millisecond)
		}
	}
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

func (a *whatsappAdapter) ChatID() string { return "" }

// ---------------------------------------------------------------------------
// Typing indicator (optional)
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
	return a.client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
}

func (a *whatsappAdapter) SupportsTyping() bool { return true }

// ---------------------------------------------------------------------------
// Start / connection lifecycle
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) Start(ctx context.Context) {
	ctx, a.cancel = context.WithCancel(ctx)

	dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
	container, err := sqlstore.New(ctx, "sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", dbPath), waLog.Noop)
	if err != nil {
		debug.Log("whatsapp", "adapter %q: open store: %v", a.name, err)
		a.publishState(false, "error", fmt.Sprintf("store: %v", err))
		return
	}
	a.storeContainer = container

	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		debug.Log("whatsapp", "adapter %q: get devices: %v", a.name, err)
		a.publishState(false, "error", fmt.Sprintf("devices: %v", err))
		return
	}
	if len(devices) > 0 {
		a.device = devices[0]
	} else {
		a.device = container.NewDevice()
	}

	a.client = whatsmeow.NewClient(a.device, waLog.Noop)
	a.client.AddEventHandler(a.eventHandler())

	if a.client.Store.ID == nil {
		// No session — need QR login
		debug.Log("whatsapp", "adapter %q: no session, requesting QR code", a.name)
		a.publishState(false, "pairing", "scan QR code with WhatsApp")
		qrChan, _ := a.client.GetQRChannel(ctx)
		if err := a.client.Connect(); err != nil {
			debug.Log("whatsapp", "adapter %q: connect: %v", a.name, err)
			return
		}
		if qrChan != nil {
			for evt := range qrChan {
				if evt.Event == "code" {
					debug.Log("whatsapp", "adapter %q: QR code generated", a.name)
					img, _ := qrcode.New(evt.Code, qrcode.Medium)
					fmt.Fprint(os.Stderr, string(img.ToSmallString(false)))
				}
			}
		}
	} else {
		debug.Log("whatsapp", "adapter %q: connecting with saved session", a.name)
		if err := a.client.Connect(); err != nil {
			debug.Log("whatsapp", "adapter %q: connect: %v", a.name, err)
			return
		}
	}

	<-ctx.Done()
	a.client.Disconnect()
	_ = a.storeContainer.Close()
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
			a.mu.Unlock()
			debug.Log("whatsapp", "adapter %q: connected", a.name)
			a.publishState(true, "", "")

		case *events.Disconnected:
			a.mu.Lock()
			a.connected = false
			a.mu.Unlock()
			debug.Log("whatsapp", "adapter %q: disconnected", a.name)
			a.publishState(false, "disconnected", "")

		case *events.LoggedOut:
			a.mu.Lock()
			a.connected = false
			a.mu.Unlock()
			debug.Log("whatsapp", "adapter %q: logged out: %s", a.name, v.Reason)
			a.publishState(false, "logged_out", "need re-pairing")

		case *events.PairSuccess:
			debug.Log("whatsapp", "adapter %q: paired (JID: %s)", a.name, v.ID)

		case *events.Message:
			a.handleInbound(v)
		}
	}
}

// ---------------------------------------------------------------------------
// Inbound
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) handleInbound(msg *events.Message) {
	if msg.Info.IsFromMe {
		return
	}

	text := ""
	if conv := msg.Message.GetConversation(); conv != "" {
		text = conv
	} else if ext := msg.Message.GetExtendedTextMessage(); ext != nil {
		text = ext.GetText()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	sender := msg.Info.Sender.String()
	chatID := msg.Info.Chat.String()

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
	return err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *whatsappAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformWhatsApp,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}

// chunkWAText splits text into chunks at most maxLen, preferring newline boundaries.
func chunkWAText(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > maxLen {
		boundary := strings.LastIndex(text[:maxLen], "\n")
		if boundary <= 0 {
			boundary = maxLen
		}
		chunks = append(chunks, text[:boundary])
		text = strings.TrimPrefix(text[boundary:], "\n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
