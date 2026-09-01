package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	imagepkg "github.com/topcheer/ggcode/internal/image"

	"go.mau.fi/util/dbutil"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/yuin/goldmark"
)

const (
	matrixMaxMessageLen = 60000 // Synapse default max_event_size is 100KB; 60K leaves room for event JSON metadata
	matrixSyncTimeout   = 30000
	matrixCryptoDBName  = "matrix-crypto.db"
	// matrixCryptoPickleKey encrypts crypto material at rest in the
	// SQLite store. Device-identity continuity, not secrecy against the
	// local user, is the goal (#1404-A); a fixed key keeps the store
	// portable across restarts without a new secrets bootstrap path.
	matrixCryptoPickleKey = "ggcode-matrix-crypto-v1"

	// matrixInterMessageDelay is the delay between consecutive messages to the
	// same room. Most Matrix homeservers rate-limit at ~1 message/second/user.
	// Source: https://spec.matrix.org/latest/client-server-api/#rate-limiting
	matrixInterMessageDelay = 500 * time.Millisecond

	// matrixMaxRetries is the maximum number of retries on M_LIMIT_EXCEEDED.
	matrixMaxRetries = 3
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

	// First sync flag - ignore events from initial sync
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

func (a *matrixAdapter) isClosed() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.closed
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
		if ctx.Err() != nil || a.isClosed() {
			a.publishState(false, "stopped", "")
			return
		}

		startedAt := time.Now()
		err := a.runOnce(ctx)
		if err == nil {
			// Clean shutdown
			return
		}

		if a.isClosed() {
			return
		}

		// #432: after a HEALTHY connection (sync stayed up for a while),
		// the next failure is a fresh transient - reset backoff like the
		// Discord adapter's #389 fix instead of resuming the accumulated
		// 60s wait after hours of stable connectivity.
		if time.Since(startedAt) >= 60*time.Second {
			backoff = 5 * time.Second
		}

		debug.Log("matrix", "adapter=%s runOnce failed: %v, retrying in %v", a.name, err, backoff)
		a.publishState(false, "reconnecting", err.Error())

		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-time.After(jitterDuration(backoff)):
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
	// #433: a.client/a.userID are read concurrently by Send/sendImage/
	// TriggerTyping on other goroutines - writes must hold a.mu.
	a.mu.Lock()
	a.client = client
	a.mu.Unlock()

	// 2. Whoami to verify token
	whoami, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	a.mu.Lock()
	a.userID = string(whoami.UserID)
	a.mu.Unlock()
	client.UserID = whoami.UserID
	client.DeviceID = whoami.DeviceID
	debug.Log("matrix", "adapter=%s authenticated as %s device=%s", a.name, a.userID, client.DeviceID)

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

// openPersistentCryptoStore opens the SQLite-backed crypto store so the
// Olm device identity, sessions and Megolm room keys survive reconnects
// and restarts (#1404-A). The file is keyed by adapter name under the
// workspace config dir; accountID is derived from homeserver+name so two
// adapters never share a device identity.
func (a *matrixAdapter) openPersistentCryptoStore() (crypto.Store, error) {
	dir := filepath.Join(config.ConfigDir(), "matrix-crypto")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create crypto state dir: %w", err)
	}
	dbPath := filepath.Join(dir, fmt.Sprintf("%s.db", sanitizeFileToken(a.name)))
	uri := fmt.Sprintf("sqlite3://file:%s?_txlock=immediate", dbPath)
	db, err := dbutil.NewWithDialect(uri, "sqlite3")
	if err != nil {
		return nil, fmt.Errorf("open crypto db %s: %w", dbPath, err)
	}
	accountID := fmt.Sprintf("%s|%s", a.homeserver, a.name)
	deviceID := a.client.DeviceID
	return crypto.NewSQLCryptoStore(db, nil, accountID, deviceID, []byte(matrixCryptoPickleKey)), nil
}

// sanitizeFileToken reduces an adapter name to a filename-safe token.
func sanitizeFileToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (a *matrixAdapter) setupCrypto(ctx context.Context) error {
	// #1404-A: this used to be crypto.NewMemoryStore(nil) - a fresh Olm
	// device identity on EVERY runOnce re-entry (any transient sync error
	// backing off, not just process restarts). Consequences: peers had to
	// re-verify the "new" device (Element warns on unverified devices and
	// may withhold messages), and offline-period Megolm sessions were
	// undecryptable with no key-forwarding state - messages failed to
	// decrypt and were silently dropped (debug.Log + return). The
	// matrixCryptoDBName constant existed unused - persistence was planned
	// but never landed. SQLite-backed SQLCryptoStore now keeps the device
	// identity, sessions and sync token across restarts/reconnects; when
	// the state file cannot be opened we fall back to MemoryStore rather
	// than losing the adapter entirely.
	store, err := a.openPersistentCryptoStore()
	if err != nil {
		debug.Log("matrix", "adapter=%s persistent crypto store unavailable (%v) - falling back to in-memory (E2EE identity will rotate on reconnect)", a.name, err)
		store = crypto.NewMemoryStore(nil)
	}

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
			if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
				a.publishState(false, "warning", err.Error())
			}
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
	// Authoritative source: m.direct account data (same semantics as
	// fetchDMRooms). A room marked there IS a DM regardless of membership.
	var dmMap map[string][]string
	if err := a.client.GetAccountData(ctx, "m.direct", &dmMap); err == nil {
		for _, rooms := range dmMap {
			for _, rid := range rooms {
				if rid == roomID {
					debug.Log("matrix", "adapter=%s room=%s is DM via m.direct account data", a.name, roomID)
					return true
				}
			}
		}
	} else {
		debug.Log("matrix", "adapter=%s m.direct lookup room=%s error: %v", a.name, roomID, err)
	}
	// Heuristic fallback: exactly 2 joined members. Deliberately NOT cached -
	// a 2-member room may be a small group rather than a DM, and permanently
	// caching it as DM would bypass the mention gate irrevocably and could be
	// induced by anyone inviting the bot into a 2-person room (issue #961).
	members, err := a.client.JoinedMembers(ctx, id.RoomID(roomID))
	if err != nil {
		debug.Log("matrix", "adapter=%s checkIsDMViaAPI room=%s error: %v", a.name, roomID, err)
		return false
	}
	if len(members.Joined) == 2 {
		debug.Log("matrix", "adapter=%s room=%s looks like DM (2 members, heuristic, not cached)", a.name, roomID)
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

	// Extract images from markdown text and send them as m.image events.
	images, remainingText := ExtractImagesFromText(text)
	for i, img := range images {
		data, mimeType, err := a.resolveImageToBytes(ctx, img)
		if err != nil {
			debug.Log("matrix", "adapter=%s image resolve failed [%d/%d]: %v", a.name, i+1, len(images), err)
			continue
		}
		if err := a.sendImage(ctx, chatID, binding.ThreadID, data, mimeType); err != nil {
			debug.Log("matrix", "adapter=%s image send failed [%d/%d]: %v", a.name, i+1, len(images), err)
		} else {
			debug.Log("matrix", "adapter=%s image sent [%d/%d] mime=%s size=%d", a.name, i+1, len(images), mimeType, len(data))
		}
		// Rate limit between image sends
		select {
		case <-time.After(matrixInterMessageDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if remainingText == "" {
		return nil
	}

	debug.Log("matrix", "adapter=%s send room=%s kind=%s len=%d images=%d", a.name, chatID, event.Kind, len(remainingText), len(images))
	err := a.sendText(ctx, chatID, binding.ThreadID, remainingText)
	if err != nil {
		debug.Log("matrix", "adapter=%s send FAILED room=%s: %v", a.name, chatID, err)
	}
	return err
}

// resolveImageToBytes resolves an ExtractedImage to raw bytes and MIME type.
func (a *matrixAdapter) resolveImageToBytes(ctx context.Context, img ExtractedImage) ([]byte, string, error) {
	switch img.Kind {
	case "data":
		// data:image/png;base64,XXXXX
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("invalid data URL")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, "", fmt.Errorf("invalid base64 in data URL: %w", err)
		}
		decoded, err := imagepkg.Decode(data)
		if err != nil {
			return nil, "", fmt.Errorf("data URL is not a valid image: %w", err)
		}
		return data, decoded.MIME, nil

	case "url":
		// #1016: ExtractImagesFromText emits local paths as Kind "url"; route
		// them to local read instead of an HTTP request that would fail with
		// "unsupported protocol scheme" (the old case "local" below was dead -
		// no extractor ever emitted Kind "local").
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return nil, "", fmt.Errorf("read local image: %w", err)
			}
			decoded, err := imagepkg.Decode(data)
			if err != nil {
				return nil, "", fmt.Errorf("decode local image: %w", err)
			}
			return data, decoded.MIME, nil
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.Data, nil)
		if err != nil {
			return nil, "", fmt.Errorf("create request: %w", err)
		}
		resp, err := imageDownloadClient.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("download image: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, "", fmt.Errorf("download image: HTTP %d", resp.StatusCode)
		}
		data, err := imagepkg.ReadLimited(resp.Body, imagepkg.MaxSize)
		if err != nil {
			return nil, "", fmt.Errorf("read image response: %w", err)
		}
		mimeType := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(mimeType, "image/") {
			mimeType = imagepkg.DetectMIME(data)
		}
		if !strings.HasPrefix(mimeType, "image/") {
			return nil, "", fmt.Errorf("content is not an image: %s", mimeType)
		}
		return data, mimeType, nil

	default:
		return nil, "", fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// matrixRetryAfterMaxMS caps server-provided retry_after_ms values (#664).
// 24h in milliseconds - a sane upper bound for a homeserver backoff.
const matrixRetryAfterMaxMS = 24 * 60 * 60 * 1000

// matrixRetryAfter converts a server-provided retry_after_ms value (already
// verified > 0 by the caller) into a Duration, clamping to
// matrixRetryAfterMaxMS BEFORE the float→Duration multiplication. Without
// the clamp, retry_after_ms above ~9.22e12 (or +Inf) wraps to a large
// negative duration, and time.After(negative) fires immediately - a zero-
// delay hammering that ignores the server's backoff signal (bounded by
// matrixMaxRetries, but still 4 rapid hits per send).
func matrixRetryAfter(msFloat float64) time.Duration {
	if msFloat > matrixRetryAfterMaxMS {
		msFloat = matrixRetryAfterMaxMS
	}
	return time.Duration(msFloat) * time.Millisecond
}

// sendImage uploads image data to the Matrix homeserver and sends an m.image event.
func (a *matrixAdapter) sendImage(ctx context.Context, roomID, threadID string, data []byte, mimeType string) error {
	// #433: snapshot the client under the read lock - reconnect writes it.
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("matrix adapter not connected")
	}

	// Upload to Matrix content repository
	resp, err := client.UploadMedia(ctx, mautrix.ReqUploadMedia{
		ContentBytes: data,
		ContentType:  mimeType,
		FileName:     "image",
	})
	if err != nil {
		return fmt.Errorf("upload media: %w", err)
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgImage,
		Body:    "image",
		URL:     resp.ContentURI.CUString(),
		Info: &event.FileInfo{
			MimeType: mimeType,
			Size:     len(data),
		},
	}

	if threadID != "" {
		content.RelatesTo = &event.RelatesTo{
			Type:    event.RelThread,
			EventID: id.EventID(threadID),
		}
	}

	txnID := fmt.Sprintf("ggcode-img-%d", a.txnID.Add(1))
	for attempt := 0; attempt <= matrixMaxRetries; attempt++ {
		_, err = client.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage, content, mautrix.ReqSendEvent{TransactionID: txnID})
		if err == nil {
			return nil
		}
		var respErr *mautrix.RespError
		if errors.As(err, &respErr) && respErr.ErrCode == "M_LIMIT_EXCEEDED" && attempt < matrixMaxRetries {
			retryAfter := matrixInterMessageDelay * 2
			if ms, ok := respErr.ExtraData["retry_after_ms"]; ok {
				// #664: clamp BEFORE the float→Duration conversion (same family
				// as #513/#658). retry_after_ms > 9.22e12 or +Inf wraps to a
				// large negative duration and time.After(negative) fires
				// immediately, bypassing the server's backoff.
				if msFloat, ok2 := ms.(float64); ok2 && msFloat > 0 {
					retryAfter = matrixRetryAfter(msFloat)
				}
			}
			select {
			case <-time.After(retryAfter):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		return err
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
	// #433: snapshot the client under the read lock.
	a.mu.RLock()
	tClient := a.client
	a.mu.RUnlock()
	if tClient == nil {
		return fmt.Errorf("matrix adapter not connected")
	}
	reaction := reactionAckValue(PlatformMatrix, target)
	if reaction == "" {
		return nil
	}
	if _, err := tClient.SendReaction(ctx, id.RoomID(roomID), id.EventID(target), reaction); err != nil {
		debug.Log("matrix", "adapter=%s typing reaction failed room=%s target=%s: %v", a.name, roomID, target, err)
		return err
	}
	a.reactionAck.MarkSent(binding, target)
	return nil
}

func (a *matrixAdapter) sendText(ctx context.Context, roomID, threadID, text string) error {
	// #433: snapshot the client under the read lock.
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("matrix adapter not connected")
	}

	// Use markdown-aware splitting so fenced code blocks are not split mid-way.
	// Each chunk gets proper ``` open/close markers, producing valid HTML when
	// goldmark renders it. Plain rune-based splitting (chunkText) would break
	// code blocks, causing goldmark to emit malformed/incomplete HTML.
	chunks := SplitMarkdown(text, matrixMaxMessageLen)
	for i, chunk := range chunks {
		// Rate limit: most homeservers limit ~1 msg/sec/user.
		if i > 0 {
			select {
			case <-time.After(matrixInterMessageDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

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
		var err error
		for attempt := 0; attempt <= matrixMaxRetries; attempt++ {
			_, err = client.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage, content, mautrix.ReqSendEvent{TransactionID: txnID})
			if err == nil {
				break
			}
			// Retry on M_LIMIT_EXCEEDED with server-provided delay.
			var respErr *mautrix.RespError
			if errors.As(err, &respErr) && respErr.ErrCode == "M_LIMIT_EXCEEDED" && attempt < matrixMaxRetries {
				retryAfter := matrixInterMessageDelay * 2
				if ms, ok := respErr.ExtraData["retry_after_ms"]; ok {
					// #664: clamp BEFORE the float→Duration conversion (same family
					// as #513/#658). retry_after_ms > 9.22e12 or +Inf wraps to a
					// large negative duration and time.After(negative) fires
					// immediately, bypassing the server's backoff.
					if msFloat, ok2 := ms.(float64); ok2 && msFloat > 0 {
						retryAfter = matrixRetryAfter(msFloat)
					}
				}
				debug.Log("matrix", "adapter=%s rate-limited (M_LIMIT_EXCEEDED), retry %d/%d after %v",
					a.name, attempt+1, matrixMaxRetries, retryAfter)
				select {
				case <-time.After(retryAfter):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			break
		}
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

// GetHistoryVisibility satisfies the mautrix v0.30 crypto.StateStore
// interface. The OlmMachine uses it to decide key-sharing scope; unknown
// rooms fall back to Matrix's default ("shared"), matching mautrix's own
// DefaultHistoryVisibility handling.
func (s *cryptoStateStore) GetHistoryVisibility(ctx context.Context, roomID id.RoomID) (*event.HistoryVisibilityEventContent, error) {
	if s.adapter.client == nil {
		return &event.HistoryVisibilityEventContent{HistoryVisibility: event.HistoryVisibilityShared}, nil
	}
	var hv event.HistoryVisibilityEventContent
	if err := s.adapter.client.StateEvent(ctx, roomID, event.StateHistoryVisibility, "", &hv); err != nil {
		// #1355: fail CLOSED, aligned with mautrix's own crypto store
		// (encryptmegolm.go returns the error on state fetch failure).
		// Returning "shared" on a transient error marked joined/invited
		// rooms SharedHistory=true; that flag then rides MSC3061
		// shared_history room keys, key forwards and key backups for the
		// outbound session's whole lifetime (~7 days / 100 messages) with
		// no correction path. The message fails to encrypt now and the
		// caller retries - correct beats silently over-sharing.
		return nil, err
	}
	if hv.HistoryVisibility == "" {
		// The state fetch SUCCEEDED but the room has no visibility event
		// (or empty content): Matrix's documented default IS "shared" -
		// a legitimate default, not an error path.
		return &event.HistoryVisibilityEventContent{HistoryVisibility: event.HistoryVisibilityShared}, nil
	}
	return &hv, nil
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

// stripMatrixReplyFallback removes the rich-reply fallback quote block from
// the TOP of a reply body, per the Matrix spec: the fallback is the leading
// run of ">" lines plus the blank-line separator that follows it. Body
// content after the separator - including the user's own blockquotes and
// code fences containing ">" lines - is preserved verbatim (#1222).
func stripMatrixReplyFallback(body string) string {
	lines := strings.Split(body, "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], ">") {
		i++
	}
	// Keep the body as-is when there is no leading fallback block, when the
	// block is not terminated by the spec's blank-line separator, or when
	// nothing follows the separator (indistinguishable from a plain quote
	// message, so nothing is stripped).
	if i == 0 || i >= len(lines) || strings.TrimSpace(lines[i]) != "" {
		return strings.TrimSpace(body)
	}
	return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
}

// minimal json import for fetchDMRooms
var _ = json.Marshal
