package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/yuin/goldmark"
)

const (
	matrixMaxMessageLen = 4000
	matrixSyncTimeout   = 30000
	matrixCryptoDBName  = "matrix-crypto.db"
)

type matrixAdapter struct {
	name     string
	manager  *Manager
	platform Platform

	// Config
	homeserver     string
	token          string
	userID         string
	requireMention bool
	freeRooms      []string
	allowedUsers   []string

	// mautrix client
	client *mautrix.Client

	// E2EE
	mach *crypto.OlmMachine

	// State
	mu        sync.RWMutex
	connected bool
	closed    bool
	cancelFn  context.CancelFunc

	// DM room cache: room_id → true
	dmRooms map[string]bool

	// Dedup
	seen map[string]time.Time

	// First sync flag — ignore events from initial sync
	didFirstSync atomic.Bool

	// Transaction ID counter for send
	txnID atomic.Int64

	reactionAck reactionAckState
}

func newMatrixAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*matrixAdapter, error) {
	homeserver := strings.TrimSpace(stringValue(adapterCfg.Extra, "homeserver"))
	if homeserver == "" {
		homeserver = strings.TrimSpace(os.Getenv("MATRIX_HOMESERVER"))
	}
	if homeserver == "" {
		homeserver = strings.TrimSpace(os.Getenv("GGCODE_IM_MATRIX_HOMESERVER"))
	}
	if homeserver == "" {
		return nil, fmt.Errorf("matrix adapter %q: missing homeserver", name)
	}

	token := strings.TrimSpace(stringValue(adapterCfg.Extra, "access_token"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("MATRIX_ACCESS_TOKEN"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GGCODE_IM_MATRIX_ACCESS_TOKEN"))
	}
	if token == "" {
		return nil, fmt.Errorf("matrix adapter %q: missing access_token", name)
	}

	userID := strings.TrimSpace(stringValue(adapterCfg.Extra, "user_id"))
	if userID == "" {
		userID = strings.TrimSpace(os.Getenv("MATRIX_USER_ID"))
	}

	requireMention := true
	if v := strings.ToLower(stringValue(adapterCfg.Extra, "require_mention")); v == "false" || v == "0" || v == "no" {
		requireMention = false
	}

	freeRooms := parseCommaList(stringValue(adapterCfg.Extra, "free_rooms"), os.Getenv("MATRIX_FREE_ROOMS"))
	allowedUsers := parseCommaList(stringValue(adapterCfg.Extra, "allowed_users"), os.Getenv("MATRIX_ALLOWED_USERS"))

	return &matrixAdapter{
		name:           name,
		manager:        mgr,
		platform:       PlatformMatrix,
		homeserver:     homeserver,
		token:          token,
		userID:         userID,
		requireMention: requireMention,
		freeRooms:      freeRooms,
		allowedUsers:   allowedUsers,
		dmRooms:        make(map[string]bool),
		seen:           make(map[string]time.Time),
	}, nil
}

func (a *matrixAdapter) Name() string       { return a.name }
func (a *matrixAdapter) Platform() Platform { return PlatformMatrix }

func (a *matrixAdapter) Start(ctx context.Context) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	a.publishState(false, "connecting", "")
	safego.Go("im.matrix.run", func() { a.run(ctx) })
}

func (a *matrixAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.connected = false
	if a.cancelFn != nil {
		a.cancelFn()
	}
	return nil
}

func (a *matrixAdapter) run(ctx context.Context) {
	debug.Log("matrix", "adapter=%s start", a.name)

	backoff := 5 * time.Second
	maxBackoff := 60 * time.Second

	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}

		err := a.runOnce(ctx)
		if err == nil {
			// Clean shutdown
			return
		}

		debug.Log("matrix", "adapter=%s runOnce failed: %v, retrying in %v", a.name, err, backoff)
		a.publishState(false, "reconnecting", err.Error())

		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-time.After(backoff):
		}

		backoff = backoff * 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (a *matrixAdapter) runOnce(ctx context.Context) error {
	// 1. Create mautrix client
	client, err := mautrix.NewClient(a.homeserver, id.UserID(a.userID), a.token)
	if err != nil {
		return fmt.Errorf("client init: %w", err)
	}
	a.client = client

	// 2. Whoami to verify token
	whoami, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	a.userID = string(whoami.UserID)
	a.client.UserID = whoami.UserID
	a.client.DeviceID = whoami.DeviceID
	debug.Log("matrix", "adapter=%s authenticated as %s device=%s", a.name, a.userID, a.client.DeviceID)

	// 3. Setup E2EE crypto
	if err := a.setupCrypto(ctx); err != nil {
		debug.Log("matrix", "adapter=%s crypto setup failed (continuing without E2EE): %v", a.name, err)
		// Non-fatal: continue without crypto support
	}

	// 4. Fetch DM rooms
	a.fetchDMRooms(ctx)

	// 5. Setup syncer
	syncer := mautrix.NewDefaultSyncer()

	// Auto-join when invited
	syncer.OnEventType(event.StateMember, func(ctx context.Context, evt *event.Event) {
		membership, _ := evt.Content.Raw["membership"].(string)
		if membership == "invite" && evt.GetStateKey() == string(a.client.UserID) {
			debug.Log("matrix", "adapter=%s invited to room=%s, joining", a.name, evt.RoomID)
			_, err := a.client.JoinRoom(ctx, evt.RoomID.String(), nil)
			if err != nil {
				debug.Log("matrix", "adapter=%s failed to join room=%s: %v", a.name, evt.RoomID, err)
			} else {
				debug.Log("matrix", "adapter=%s joined room=%s", a.name, evt.RoomID)
			}
		}
	})

	// OnSync: mark first sync done + process crypto to-device events
	syncer.OnSync(func(ctx context.Context, resp *mautrix.RespSync, since string) bool {
		if since != "" {
			a.didFirstSync.Store(true)
		}
		if a.mach != nil {
			return a.mach.ProcessSyncResponse(ctx, resp, since)
		}
		return true
	})

	// Mark first sync done after initial sync completes
	syncer.OnEvent(func(ctx context.Context, evt *event.Event) {
		// Feed member/encryption events to OlmMachine for crypto tracking
		if a.mach != nil {
			if evt.Type == event.StateMember {
				a.mach.HandleMemberEvent(ctx, evt)
			}
		}
		// Skip events from initial sync (before we have a "since" token)
		if !a.didFirstSync.Load() {
			debug.Log("matrix", "adapter=%s skipping initial sync event type=%s", a.name, evt.Type.Type)
			return
		}
		a.handleEvent(ctx, evt)
	})

	client.Syncer = syncer

	a.publishState(true, "connected", "")
	debug.Log("matrix", "adapter=%s entering sync loop", a.name)

	// 6. Run sync (blocking)
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancelFn = cancel
	a.mu.Unlock()

	err = client.SyncWithContext(ctx)
	if err != nil && ctx.Err() == nil {
		debug.Log("matrix", "adapter=%s sync stopped with error: %v", a.name, err)
		return fmt.Errorf("sync: %w", err)
	}
	return nil
}

func (a *matrixAdapter) setupCrypto(ctx context.Context) error {
	store := crypto.NewMemoryStore(nil)

	mach := crypto.NewOlmMachine(a.client, nil, store, &cryptoStateStore{adapter: a})

	if err := mach.Load(ctx); err != nil {
		return fmt.Errorf("load olm machine: %w", err)
	}

	// Upload device keys and one-time keys so other users can send us encrypted messages
	if err := mach.ShareKeys(ctx, -1); err != nil {
		debug.Log("matrix", "adapter=%s ShareKeys failed: %v", a.name, err)
		// Non-fatal: keys may already be uploaded
	}

	a.mach = mach
	debug.Log("matrix", "adapter=%s E2EE crypto loaded (device=%s)", a.name, a.client.DeviceID)
	return nil
}

func (a *matrixAdapter) fetchDMRooms(ctx context.Context) {
	if a.client == nil {
		return
	}
	var dmMap map[string][]string
	err := a.client.GetAccountData(ctx, "m.direct", &dmMap)
	if err != nil {
		debug.Log("matrix", "adapter=%s fetchDMRooms error: %v", a.name, err)
		return
	}

	a.mu.Lock()
	for _, rooms := range dmMap {
		for _, roomID := range rooms {
			a.dmRooms[roomID] = true
		}
	}
	a.mu.Unlock()
	debug.Log("matrix", "adapter=%s found %d DM rooms", a.name, len(a.dmRooms))
}

func (a *matrixAdapter) handleEvent(ctx context.Context, evt *event.Event) {
	roomID := string(evt.RoomID)
	sender := string(evt.Sender)

	debug.Log("matrix", "adapter=%s handleEvent room=%s sender=%s type=%s", a.name, roomID, sender, evt.Type.Type)

	// Handle E2EE: decrypt encrypted events
	if evt.Type == event.EventEncrypted {
		if a.mach == nil {
			debug.Log("matrix", "adapter=%s encrypted event but no crypto machine, dropping", a.name)
			return
		}
		decrypted, err := a.mach.DecryptMegolmEvent(ctx, evt)
		if err != nil {
			debug.Log("matrix", "adapter=%s decrypt failed room=%s: %v", a.name, roomID, err)
			return
		}
		debug.Log("matrix", "adapter=%s decrypted event in room=%s -> type=%s", a.name, roomID, decrypted.Type.Type)
		evt = decrypted
	}

	// Only handle text messages
	if evt.Type != event.EventMessage {
		debug.Log("matrix", "adapter=%s skipping non-message type=%s", a.name, evt.Type.Type)
		return
	}

	// Skip own messages
	if sender == a.userID {
		return
	}

	// Dedup
	eventID := string(evt.ID)
	now := time.Now()
	a.mu.Lock()
	if t, ok := a.seen[eventID]; ok && now.Sub(t) < 5*time.Minute {
		a.mu.Unlock()
		return
	}
	a.seen[eventID] = now
	for k, v := range a.seen {
		if now.Sub(v) > 10*time.Minute {
			delete(a.seen, k)
		}
	}
	a.mu.Unlock()

	// Extract content
	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok {
		debug.Log("matrix", "adapter=%s content not parsed, trying raw (hasRaw=%v)", a.name, evt.Content.Raw != nil)
		var rawContent struct {
			MsgType string `json:"msgtype"`
			Body    string `json:"body"`
		}
		rawBytes, _ := json.Marshal(evt.Content.Raw)
		if err := json.Unmarshal(rawBytes, &rawContent); err != nil {
			debug.Log("matrix", "adapter=%s raw content parse error: %v", a.name, err)
			return
		}
		if rawContent.MsgType != "m.text" && rawContent.MsgType != "" {
			debug.Log("matrix", "adapter=%s skipping non-text raw msgtype=%s", a.name, rawContent.MsgType)
			return
		}
		content = &event.MessageEventContent{
			MsgType: event.MessageType(rawContent.MsgType),
			Body:    rawContent.Body,
		}
	}

	msgtype := string(content.MsgType)
	body := content.Body

	if msgtype != "m.text" && msgtype != "" {
		debug.Log("matrix", "adapter=%s skipping msgtype=%s", a.name, msgtype)
		return
	}

	// Strip reply fallback
	if content.GetReplyTo() != "" {
		body = stripMatrixReplyFallback(body)
	}

	debug.Log("matrix", "adapter=%s message room=%s sender=%s body=%.80s", a.name, roomID, sender, body)

	// Allowed users check
	if len(a.allowedUsers) > 0 && !entryMatches(a.allowedUsers, sender) {
		debug.Log("matrix", "adapter=%s sender=%s not in allowed_users, dropping", a.name, sender)
		return
	}

	// DM detection
	a.mu.RLock()
	isDM := a.dmRooms[roomID]
	dmCount := len(a.dmRooms)
	a.mu.RUnlock()
	if !isDM {
		isDM = a.checkIsDMViaAPI(ctx, roomID)
	}
	debug.Log("matrix", "adapter=%s room=%s isDM=%v dmRooms=%d", a.name, roomID, isDM, dmCount)

	// Mention gating for non-DM rooms
	if !isDM {
		isFree := entryMatches(a.freeRooms, roomID)
		if !isFree && a.requireMention {
			hasMention := a.hasMention(body, evt.Content.Raw)
			debug.Log("matrix", "adapter=%s non-DM room=%s free=%v mention=%v requireMention=%v", a.name, roomID, isFree, hasMention, a.requireMention)
			if !hasMention {
				return
			}
			body = a.stripMention(body)
		}
	}

	// Build inbound message
	displayName := a.getDisplayName(ctx, sender)
	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformMatrix,
			ChannelID:  roomID,
			SenderID:   sender,
			SenderName: displayName,
			MessageID:  eventID,
			ReceivedAt: time.Now(),
		},
		Text: strings.TrimSpace(body),
	}

	debug.Log("matrix", "adapter=%s -> HandlePairingInbound", a.name)

	// Pairing flow
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("matrix", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err.Error() != "no session bound" {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendText(ctx, roomID, "", pairingResult.ReplyText)
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, msg)
	}
}

// --- DM detection ---

func (a *matrixAdapter) checkIsDMViaAPI(ctx context.Context, roomID string) bool {
	if a.client == nil {
		return false
	}
	members, err := a.client.JoinedMembers(ctx, id.RoomID(roomID))
	if err != nil {
		debug.Log("matrix", "adapter=%s checkIsDMViaAPI room=%s error: %v", a.name, roomID, err)
		return false
	}
	if len(members.Joined) == 2 {
		a.mu.Lock()
		a.dmRooms[roomID] = true
		a.mu.Unlock()
		debug.Log("matrix", "adapter=%s room=%s detected as DM via API (2 members)", a.name, roomID)
		return true
	}
	debug.Log("matrix", "adapter=%s room=%s API: %d members", a.name, roomID, len(members.Joined))
	return false
}

// --- Display names ---

func (a *matrixAdapter) getDisplayName(ctx context.Context, userID string) string {
	if a.client == nil {
		return userID
	}
	resp, err := a.client.GetProfile(ctx, id.UserID(userID))
	if err != nil {
		return userID
	}
	if resp.DisplayName != "" {
		return resp.DisplayName
	}
	return userID
}

// --- Mention handling ---

func (a *matrixAdapter) hasMention(body string, raw map[string]any) bool {
	lower := strings.ToLower(body)

	// Check m.mentions.user_ids (MSC3952)
	if mentions, _ := raw["m.mentions"].(map[string]any); mentions != nil {
		if uids, _ := mentions["user_ids"].([]any); uids != nil {
			for _, uid := range uids {
				if uid == a.userID {
					return true
				}
			}
		}
	}

	// Check for @user:server in body
	if strings.Contains(lower, strings.ToLower(a.userID)) {
		return true
	}

	// Check formatted_body
	if fb, _ := raw["formatted_body"].(string); fb != "" {
		if strings.Contains(strings.ToLower(fb), strings.ToLower(a.userID)) {
			return true
		}
	}

	// @localpart in body
	localPart := a.userID
	if idx := strings.Index(localPart, ":"); idx > 0 {
		localPart = localPart[1:idx] // strip @ and :domain
	}
	if localPart != "" && strings.Contains(lower, strings.ToLower(localPart)) {
		return true
	}

	return false
}

func (a *matrixAdapter) stripMention(text string) string {
	// Strip @user_id:domain
	if a.userID != "" {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(a.userID))
		text = re.ReplaceAllString(text, "")
	}
	// Strip @localpart
	localPart := a.userID
	if idx := strings.Index(localPart, ":"); idx > 0 {
		localPart = localPart[1:idx]
	}
	if localPart != "" {
		re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(localPart))
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}

// --- Outbound ---

func (a *matrixAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	chatID := binding.ChannelID
	if chatID == "" {
		chatID = binding.TargetID
	}

	text := a.outboundText(event)
	if text == "" {
		return nil
	}

	debug.Log("matrix", "adapter=%s send room=%s kind=%s len=%d", a.name, chatID, event.Kind, len(text))
	err := a.sendText(ctx, chatID, binding.ThreadID, text)
	if err != nil {
		debug.Log("matrix", "adapter=%s send FAILED room=%s: %v", a.name, chatID, err)
	}
	return err
}

func (a *matrixAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

func (a *matrixAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	roomID := strings.TrimSpace(binding.ChannelID)
	if roomID == "" {
		roomID = strings.TrimSpace(binding.TargetID)
	}
	target := LastReactionTargetMessageID(binding)
	if roomID == "" || target == "" || !a.reactionAck.NeedsSend(binding, target) {
		return nil
	}
	if a.client == nil {
		return fmt.Errorf("matrix adapter not connected")
	}
	reaction := reactionAckValue(PlatformMatrix, target)
	if reaction == "" {
		return nil
	}
	if _, err := a.client.SendReaction(ctx, id.RoomID(roomID), id.EventID(target), reaction); err != nil {
		debug.Log("matrix", "adapter=%s typing reaction failed room=%s target=%s: %v", a.name, roomID, target, err)
		return err
	}
	a.reactionAck.MarkSent(binding, target)
	return nil
}

func (a *matrixAdapter) sendText(ctx context.Context, roomID, threadID, text string) error {
	if a.client == nil {
		return fmt.Errorf("matrix adapter not connected")
	}

	chunks := chunkText(text, matrixMaxMessageLen)
	for _, chunk := range chunks {
		content := &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    chunk,
		}

		// Render markdown to HTML for rich display in Element
		var htmlBuf bytes.Buffer
		if err := goldmark.Convert([]byte(chunk), &htmlBuf); err == nil && htmlBuf.Len() > 0 {
			content.Format = event.FormatHTML
			content.FormattedBody = htmlBuf.String()
		}

		if threadID != "" {
			content.RelatesTo = &event.RelatesTo{
				Type:    event.RelThread,
				EventID: id.EventID(threadID),
			}
		}

		txnID := fmt.Sprintf("ggcode-%d", a.txnID.Add(1))
		_, err := a.client.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage, content, mautrix.ReqSendEvent{TransactionID: txnID})
		if err != nil {
			return fmt.Errorf("matrix send to %s: %w", roomID, err)
		}
	}
	return nil
}

func chunkText(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, true)
}

func (a *matrixAdapter) publishState(healthy bool, status, lastErr string) {
	a.mu.Lock()
	a.connected = healthy
	a.mu.Unlock()
	contactURI := ""
	if a.userID != "" {
		contactURI = "https://matrix.to/#/" + a.userID
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformMatrix,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		UpdatedAt:  time.Now(),
	})
}

// --- cryptoStateStore implements crypto.StateStore ---

type cryptoStateStore struct {
	adapter *matrixAdapter
}

func (s *cryptoStateStore) IsEncrypted(ctx context.Context, roomID id.RoomID) (bool, error) {
	if s.adapter.client == nil {
		return false, nil
	}
	stateEvts, err := s.adapter.client.FullStateEvent(ctx, roomID, event.StateEncryption, "")
	if err != nil {
		return false, err
	}
	return stateEvts != nil, nil
}

func (s *cryptoStateStore) GetEncryptionEvent(ctx context.Context, roomID id.RoomID) (*event.EncryptionEventContent, error) {
	if s.adapter.client == nil {
		return nil, nil
	}
	evt, err := s.adapter.client.FullStateEvent(ctx, roomID, event.StateEncryption, "")
	if err != nil {
		return nil, err
	}
	if evt == nil {
		return nil, nil
	}
	content := &event.EncryptionEventContent{}
	err = evt.Content.ParseRaw(event.StateEncryption)
	if err != nil {
		return nil, err
	}
	content, ok := evt.Content.Parsed.(*event.EncryptionEventContent)
	if !ok {
		return nil, nil
	}
	return content, nil
}

func (s *cryptoStateStore) FindSharedRooms(ctx context.Context, userID id.UserID) ([]id.RoomID, error) {
	return nil, nil
}

func (s *cryptoStateStore) GetRoomJoinedOrInvitedMembers(ctx context.Context, roomID id.RoomID) ([]id.UserID, error) {
	if s.adapter.client == nil {
		return nil, nil
	}
	members, err := s.adapter.client.JoinedMembers(ctx, roomID)
	if err != nil {
		return nil, err
	}
	var users []id.UserID
	for u := range members.Joined {
		users = append(users, u)
	}
	return users, nil
}

// --- Helpers ---

var matrixReplyFallbackRegex = regexp.MustCompile(`(?m)^>.*\n`)

func stripMatrixReplyFallback(body string) string {
	stripped := matrixReplyFallbackRegex.ReplaceAllString(body, "")
	// Remove the blank line that follows the fallback
	stripped = strings.TrimPrefix(stripped, "\n")
	return strings.TrimSpace(stripped)
}

// minimal json import for fetchDMRooms
var _ = json.Marshal
