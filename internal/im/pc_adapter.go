package im

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const PlatformPrivateClaw Platform = "privateclaw"

// PCAdapterAPI is the exported interface for PrivateClaw adapter operations (used by TUI).
type PCAdapterAPI interface {
	CreateSession(ctx context.Context, label string, groupMode bool) (*PCInvite, string, error)
	ListSessions() []PCSessionInfo
	GetSession(sessionID string) (*pcSession, bool)
	CloseSession(sessionID string) error
	RenewSession(ctx context.Context, sessionID string) error
	GetInviteURI(sessionID string) (string, error)
	KickParticipant(sessionID, appID string) error
}

// Ensure pcAdapter satisfies PCAdapterAPI at compile time.
var _ PCAdapterAPI = (*pcAdapter)(nil)

const (
	pcDefaultRelayWSScheme = "wss"
	pcDefaultRelayHost     = "relay.privateclaw.us"
	pcDefaultRelayPath     = "/ws/provider"
)

func pcResolveRelayWSURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return fmt.Sprintf("%s://%s%s", pcDefaultRelayWSScheme, pcDefaultRelayHost, pcDefaultRelayPath)
	}
	// If already a ws:// or wss:// URL, use as-is
	if strings.HasPrefix(baseURL, "ws://") || strings.HasPrefix(baseURL, "wss://") {
		return baseURL
	}
	// Strip trailing slash
	baseURL = strings.TrimRight(baseURL, "/")
	// Convert https:// to wss://, http:// to ws://
	if strings.HasPrefix(baseURL, "https://") {
		return fmt.Sprintf("wss://%s%s", baseURL[8:], pcDefaultRelayPath)
	}
	if strings.HasPrefix(baseURL, "http://") {
		return fmt.Sprintf("ws://%s%s", baseURL[7:], pcDefaultRelayPath)
	}
	return fmt.Sprintf("wss://%s%s", baseURL, pcDefaultRelayPath)
}

func pcResolveAppWsURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "wss://relay.privateclaw.us/ws/app"
	}
	if strings.HasPrefix(baseURL, "ws://") || strings.HasPrefix(baseURL, "wss://") {
		return baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasPrefix(baseURL, "https://") {
		return fmt.Sprintf("wss://%s/ws/app", baseURL[8:])
	}
	if strings.HasPrefix(baseURL, "http://") {
		return fmt.Sprintf("ws://%s/ws/app", baseURL[7:])
	}
	return fmt.Sprintf("wss://%s/ws/app", baseURL)
}

type pcAdapter struct {
	name    string
	manager *Manager

	mu sync.RWMutex

	// Context
	ctx context.Context

	// Relay connection
	client *pcRelayClient

	// Sessions
	sessions sync.Map // sessionID → *pcSession

	// Persistence
	sessionStore PCSessionStore

	// Config
	relayBaseURL   string
	providerLabel  string
	sessionTTLMs   int
	welcomeMessage string
	providerID     string
}

func newPCAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager, sessionStore PCSessionStore) (*pcAdapter, error) {
	extra := adapterCfg.Extra

	relayBaseURL := strings.TrimSpace(stringValue(extra, "relay_base_url", "PRIVATECLAW_RELAY_BASE_URL"))
	if relayBaseURL == "" {
		relayBaseURL = strings.TrimSpace(stringValue(extra, "relay_url"))
	}

	providerLabel := strings.TrimSpace(stringValue(extra, "provider_label", "PRIVATECLAW_PROVIDER_LABEL"))
	if providerLabel == "" {
		providerLabel = "ggcode"
	}

	sessionTTLMs := 0
	if v, ok := intValue(extra["session_ttl_ms"]); ok && v > 0 {
		sessionTTLMs = v
	}
	if sessionTTLMs <= 0 {
		sessionTTLMs = pcDefaultSessionTTLMS
	}

	welcomeMessage := strings.TrimSpace(stringValue(extra, "welcome_message", "PRIVATECLAW_WELCOME_MESSAGE"))
	if welcomeMessage == "" {
		welcomeMessage = "Connected to ggcode"
	}

	providerID := strings.TrimSpace(stringValue(extra, "provider_id"))
	if providerID == "" {
		providerID = pcGenerateRequestID()
	}

	adapter := &pcAdapter{
		name:           name,
		manager:        mgr,
		relayBaseURL:   relayBaseURL,
		providerLabel:  providerLabel,
		sessionTTLMs:   sessionTTLMs,
		welcomeMessage: welcomeMessage,
		providerID:     providerID,
		sessionStore:   sessionStore,
	}
	return adapter, nil
}

// Sink interface

func (a *pcAdapter) Name() string { return a.name }

func (a *pcAdapter) Start(ctx context.Context) {
	debug.Log("pc", "adapter=%s start provider=%s (lazy connect)", a.name, a.providerID)
	a.ctx = ctx
	a.loadSessionsFromStore()
	a.publishState(false, "disconnected", "")
}

// Close is a no-op for PC adapter — it uses lazy connections.
func (a *pcAdapter) Close() error { return nil }

func (a *pcAdapter) saveSessionsToStore() {
	if a.sessionStore == nil {
		return
	}
	var persisted []pcPersistedSession
	a.sessions.Range(func(key, value interface{}) bool {
		sess := value.(*pcSession)
		sess.mu.RLock()
		if !time.Now().After(sess.ExpiresAt) {
			persisted = append(persisted, pcPersistedSession{
				SessionID:  sess.Invite.SessionID,
				SessionKey: sess.Invite.SessionKey,
				AppWsURL:   sess.Invite.AppWsURL,
				ExpiresAt:  sess.ExpiresAt.Format(time.RFC3339),
				GroupMode:  sess.GroupMode,
				Label:      sess.Label,
				CreatedAt:  sess.CreatedAt.Format(time.RFC3339),
			})
		}
		sess.mu.RUnlock()
		return true
	})
	if err := a.sessionStore.SaveAll(persisted); err != nil {
		debug.Log("pc", "save sessions error: %v", err)
	}
}

func (a *pcAdapter) loadSessionsFromStore() {
	if a.sessionStore == nil {
		return
	}
	loaded, err := a.sessionStore.LoadAll()
	if err != nil {
		debug.Log("pc", "load sessions error: %v", err)
		return
	}
	now := time.Now()
	restored := 0
	for _, ps := range loaded {
		expiresAt, err := time.Parse(time.RFC3339, ps.ExpiresAt)
		if err != nil || now.After(expiresAt) {
			continue
		}
		createdAt, _ := time.Parse(time.RFC3339, ps.CreatedAt)
		if createdAt.IsZero() {
			createdAt = now
		}
		invite := PCInvite{
			Version:    1,
			SessionID:  ps.SessionID,
			SessionKey: ps.SessionKey,
			AppWsURL:   ps.AppWsURL,
			ExpiresAt:  ps.ExpiresAt,
			GroupMode:  ps.GroupMode,
		}
		sess := newPCSession(invite, ps.Label, ps.GroupMode, expiresAt)
		sess.CreatedAt = createdAt
		a.sessions.Store(ps.SessionID, sess)
		restored++
	}
	debug.Log("pc", "restored %d sessions from store (skipped %d expired)", restored, len(loaded)-restored)
}

func (a *pcAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	if err := a.ensureConnected(ctx); err != nil {
		return fmt.Errorf("PrivateClaw adapter %q: %w", a.name, err)
	}
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()

	sessionID := strings.TrimSpace(binding.TargetID)
	if sessionID == "" {
		return fmt.Errorf("PrivateClaw session is not bound")
	}

	sess, ok := a.sessions.Load(sessionID)
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	pcSess := sess.(*pcSession)

	var payload pcPayload
	switch event.Kind {
	case OutboundEventText:
		text := strings.TrimSpace(event.Text)
		if text == "" {
			return nil
		}

		// Extract images and build attachments
		images, remainingText := ExtractImagesFromText(text)
		var attachments []map[string]interface{}
		for i, img := range images {
			att, err := a.resolvePCAttachment(ctx, img, i)
			if err != nil {
				debug.Log("pc", "adapter=%s resolve attachment failed: %v", a.name, err)
				continue
			}
			attachments = append(attachments, att)
		}

		finalText := strings.TrimSpace(remainingText)
		if finalText == "" && len(attachments) == 0 {
			return nil
		}

		payload = pcPayload{
			"kind": pcKindAssistantMessage,
			"text": finalText,
		}
		if len(attachments) > 0 {
			payload["attachments"] = attachments
		}
	case OutboundEventStatus:
		status := strings.TrimSpace(event.Status)
		if status == "" {
			return nil
		}
		payload = pcPayload{
			"kind": pcKindSystemMessage,
			"text": status,
		}
	default:
		return nil
	}

	payload["messageId"] = pcBuildMessageID("msg")
	payload["sentAt"] = pcNowISO()

	envelope, err := pcEncryptPayload(sessionID, pcSess.Invite.SessionKey, payload)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	if err := client.SendFrame(sessionID, envelope, ""); err != nil {
		return fmt.Errorf("send frame: %w", err)
	}

	// Record in history
	pcSess.AppendHistory(pcConversationTurn{
		MessageID: payload["messageId"].(string),
		Role:      payloadKindToRole(pcPayloadKind(payload)),
		Text:      pcPayloadString(payload, "text"),
		SentAt:    payload["sentAt"].(string),
	})

	debug.Log("pc", "adapter=%s outbound kind=%s session=%s", a.name, event.Kind, sessionID)
	return nil
}

// Internal methods

// ensureConnected connects to the relay if not already connected.
func (a *pcAdapter) ensureConnected(ctx context.Context) error {
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client != nil {
		return nil
	}

	a.publishState(false, "connecting", "")

	wsURL := pcResolveRelayWSURL(a.relayBaseURL)
	client = newPCRelayClient(wsURL, a.providerID)
	client.onFrame = a.handleRelayFrame
	client.onSessionClosed = a.handleSessionClosed
	client.onError = func(msg string) {
		debug.Log("pc", "relay error: %s", msg)
		a.publishState(true, "error", msg)
	}

	if err := client.Connect(ctx); err != nil {
		client.Dispose()
		a.publishState(false, "error", err.Error())
		return err
	}

	a.mu.Lock()
	a.client = client
	a.mu.Unlock()

	a.publishState(true, "connected", "")
	debug.Log("pc", "adapter=%s connected to relay", a.name)
	return nil
}

func (a *pcAdapter) handleRelayFrame(sessionID string, envelope *pcEncryptedEnvelope) {
	sess, ok := a.sessions.Load(sessionID)
	if !ok {
		debug.Log("pc", "frame for unknown session %s", sessionID)
		return
	}
	pcSess := sess.(*pcSession)

	if pcSess.IsExpired() {
		debug.Log("pc", "frame for expired session %s", sessionID)
		return
	}

	payload, err := pcDecryptPayload(sessionID, pcSess.Invite.SessionKey, envelope)
	if err != nil {
		debug.Log("pc", "decrypt error for session %s: %v", sessionID, err)
		return
	}

	kind := pcPayloadKind(payload)
	debug.Log("pc", "decrypted frame session=%s kind=%s", sessionID, kind)

	switch kind {
	case pcKindClientHello:
		a.handleClientHello(sessionID, payload)
	case pcKindUserMessage:
		a.handleUserMessage(sessionID, payload)
	case pcKindSessionClose:
		a.handleSessionCloseFromApp(sessionID, payload)
	default:
		debug.Log("pc", "unknown payload kind: %s", kind)
	}
}

func (a *pcAdapter) handleClientHello(sessionID string, payload pcPayload) {
	sess, _ := a.sessions.Load(sessionID)
	if sess == nil {
		return
	}
	pcSess := sess.(*pcSession)

	appID := pcPayloadString(payload, "appId")
	displayName := pcPayloadString(payload, "displayName")
	deviceLabel := pcPayloadString(payload, "deviceLabel")

	if appID == "" {
		debug.Log("pc", "client_hello missing appId in session %s", sessionID)
		return
	}

	if pcSess.IsAppRemoved(appID) {
		debug.Log("pc", "rejected client_hello from removed app %s in session %s", appID, sessionID)
		return
	}

	pcSess.UpsertParticipant(appID, displayName, deviceLabel)
	pcSess.SetActive()

	debug.Log("pc", "client_hello session=%s appId=%s name=%s", sessionID, appID, displayName)

	// Send server_welcome
	a.sendPayload(sessionID, pcSess, pcPayload{
		"kind":         pcKindServerWelcome,
		"messageId":    pcBuildMessageID("welcome"),
		"text":         a.welcomeMessage,
		"providerName": a.providerLabel,
		"sentAt":       pcNowISO(),
	})

	// Send provider_capabilities
	a.sendPayload(sessionID, pcSess, pcPayload{
		"kind":      pcKindProvCapabilities,
		"messageId": pcBuildMessageID("cap"),
		"sentAt":    pcNowISO(),
		"capabilities": map[string]interface{}{
			"history":     true,
			"pairing":     true,
			"attachments": true,
		},
	})
}

func (a *pcAdapter) handleUserMessage(sessionID string, payload pcPayload) {
	sess, _ := a.sessions.Load(sessionID)
	if sess == nil {
		return
	}
	pcSess := sess.(*pcSession)

	text := pcPayloadString(payload, "text")
	clientMessageID := pcPayloadString(payload, "clientMessageId")
	appID := pcPayloadString(payload, "appId")
	displayName := pcPayloadString(payload, "displayName")

	// Record in history
	pcSess.AppendHistory(pcConversationTurn{
		MessageID: clientMessageID,
		Role:      "user",
		Text:      text,
		SentAt:    pcPayloadString(payload, "sentAt"),
		AppID:     appID,
	})

	// Build inbound message
	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformPrivateClaw,
			ChannelID:  sessionID,
			SenderID:   appID,
			SenderName: displayName,
			MessageID:  clientMessageID,
			ReceivedAt: time.Now(),
		},
		Text: text,
		Metadata: map[string]string{
			"pcSessionID": sessionID,
			"pcGroupMode": fmt.Sprintf("%v", pcSess.GroupMode),
		},
	}

	// Try pairing first, then regular inbound
	if a.manager != nil {
		result, err := a.manager.HandlePairingInbound(msg)
		if err == nil && result.Consumed {
			debug.Log("pc", "pairing handled message session=%s", sessionID)
			return
		}
		if err := a.manager.HandleInbound(context.Background(), msg); err != nil {
			debug.Log("pc", "handle inbound error session=%s: %v", sessionID, err)
		}
	}
}

func (a *pcAdapter) handleSessionCloseFromApp(sessionID string, payload pcPayload) {
	reason := pcPayloadString(payload, "reason")
	debug.Log("pc", "session close from app session=%s reason=%s", sessionID, reason)
	a.sessions.Delete(sessionID)
}

func (a *pcAdapter) handleSessionClosed(sessionID, reason string) {
	debug.Log("pc", "session closed by relay session=%s reason=%s", sessionID, reason)
	a.sessions.Delete(sessionID)
}

func (a *pcAdapter) sendPayload(sessionID string, sess *pcSession, payload pcPayload) error {
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("not connected")
	}

	envelope, err := pcEncryptPayload(sessionID, sess.Invite.SessionKey, payload)
	if err != nil {
		return err
	}
	return client.SendFrame(sessionID, envelope, "")
}

// CreateSession creates a new session on the relay and returns the invite.
func (a *pcAdapter) CreateSession(ctx context.Context, label string, groupMode bool) (*PCInvite, string, error) {
	if err := a.ensureConnected(ctx); err != nil {
		return nil, "", fmt.Errorf("connect relay: %w", err)
	}
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()

	sessionKey, err := pcGenerateSessionKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate session key: %w", err)
	}

	var ttlMS *int
	ttl := a.sessionTTLMs
	ttlMS = &ttl

	var gm *bool
	if groupMode {
		gm = &groupMode
	}

	resp, err := client.CreateSession(ctx, ttlMS, label, gm)
	if err != nil {
		return nil, "", fmt.Errorf("create session: %w", err)
	}

	expiresAt, _ := time.Parse(time.RFC3339, resp.ExpiresAt)
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(time.Duration(a.sessionTTLMs) * time.Millisecond)
	}

	relayBaseURL := strings.TrimSpace(a.relayBaseURL)
	appWsURL := pcResolveAppWsURL(relayBaseURL)

	invite := PCInvite{
		Version:       1,
		SessionID:     resp.SessionID,
		SessionKey:    sessionKey,
		AppWsURL:      appWsURL,
		ExpiresAt:     resp.ExpiresAt,
		GroupMode:     groupMode,
		ProviderLabel: a.providerLabel,
	}

	sess := newPCSession(invite, label, groupMode, expiresAt)
	a.sessions.Store(resp.SessionID, sess)
	a.saveSessionsToStore()

	debug.Log("pc", "session created id=%s groupMode=%v label=%s", resp.SessionID, groupMode, label)
	return &invite, resp.SessionID, nil
}

// ListSessions returns info about all managed sessions.
func (a *pcAdapter) ListSessions() []PCSessionInfo {
	var result []PCSessionInfo
	a.sessions.Range(func(key, value interface{}) bool {
		sess := value.(*pcSession)
		sess.mu.RLock()
		result = append(result, PCSessionInfo{
			SessionID:        sess.Invite.SessionID,
			State:            sess.State,
			GroupMode:        sess.GroupMode,
			Label:            sess.Label,
			ParticipantCount: len(sess.Participants),
			ExpiresAt:        sess.ExpiresAt,
		})
		sess.mu.RUnlock()
		return true
	})
	return result
}

// GetSession returns a session by ID.
func (a *pcAdapter) GetSession(sessionID string) (*pcSession, bool) {
	v, ok := a.sessions.Load(sessionID)
	if !ok {
		return nil, false
	}
	return v.(*pcSession), true
}

// CloseSession closes a session on the relay.
func (a *pcAdapter) CloseSession(sessionID string) error {
	if err := a.ensureConnected(context.Background()); err != nil {
		return err
	}
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	err := client.CloseSession(sessionID, "provider_terminated")
	a.sessions.Delete(sessionID)
	a.saveSessionsToStore()
	return err
}

// KickParticipant removes a participant from a group session.
func (a *pcAdapter) KickParticipant(sessionID, appID string) error {
	if err := a.ensureConnected(context.Background()); err != nil {
		return err
	}
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	sess, ok := a.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	sess.MarkAppRemoved(appID)
	return client.CloseApp(sessionID, appID, "participant_removed")
}

// RenewSession renews a session's TTL.
func (a *pcAdapter) RenewSession(ctx context.Context, sessionID string) error {
	if err := a.ensureConnected(ctx); err != nil {
		return err
	}
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	resp, err := client.RenewSession(ctx, sessionID, a.sessionTTLMs)
	if err != nil {
		return err
	}
	sess, ok := a.GetSession(sessionID)
	if ok {
		expiresAt, _ := time.Parse(time.RFC3339, resp.ExpiresAt)
		sess.mu.Lock()
		sess.ExpiresAt = expiresAt
		sess.mu.Unlock()
		a.saveSessionsToStore()
	}
	return nil
}

// GetInviteURI generates the invite URI for a session.
func (a *pcAdapter) GetInviteURI(sessionID string) (string, error) {
	sess, ok := a.GetSession(sessionID)
	if !ok {
		return "", fmt.Errorf("session %s not found", sessionID)
	}
	return PCEncodeInviteToURI(sess.Invite)
}

// resolvePCAttachment converts an ExtractedImage to a PrivateClaw attachment map.
func (a *pcAdapter) resolvePCAttachment(ctx context.Context, img ExtractedImage, index int) (map[string]interface{}, error) {
	att := map[string]interface{}{
		"id":   pcBuildMessageID(fmt.Sprintf("att%d", index)),
		"name": fmt.Sprintf("image_%d", index),
	}

	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return nil, fmt.Errorf("read local image: %w", err)
			}
			ext := strings.ToLower(filepath.Ext(img.Data))
			mimeType := "image/png"
			switch ext {
			case ".jpg", ".jpeg":
				mimeType = "image/jpeg"
			case ".gif":
				mimeType = "image/gif"
			case ".webp":
				mimeType = "image/webp"
			}
			att["mimeType"] = mimeType
			att["sizeBytes"] = len(data)
			att["dataBase64"] = base64.StdEncoding.EncodeToString(data)
			att["name"] = filepath.Base(img.Data)
		} else {
			// Download the image
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.Data, nil)
			if err != nil {
				return nil, err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("download image: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				return nil, fmt.Errorf("download image [%d]", resp.StatusCode)
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("read image data: %w", err)
			}
			mimeType := resp.Header.Get("Content-Type")
			if mimeType == "" {
				mimeType = "image/png"
			}
			att["mimeType"] = mimeType
			att["sizeBytes"] = len(data)
			att["dataBase64"] = base64.StdEncoding.EncodeToString(data)
			att["uri"] = img.Data
			name := filepath.Base(img.Data)
			if name != "" && name != "." {
				att["name"] = name
			}
		}
	case "data_url":
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid data URL")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid base64: %w", err)
		}
		mimeType := "image/png"
		if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
			mimeType = "image/jpeg"
		} else if strings.Contains(parts[0], "gif") {
			mimeType = "image/gif"
		} else if strings.Contains(parts[0], "webp") {
			mimeType = "image/webp"
		}
		ext := ".png"
		switch mimeType {
		case "image/jpeg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		}
		att["mimeType"] = mimeType
		att["sizeBytes"] = len(data)
		att["dataBase64"] = base64.StdEncoding.EncodeToString(data)
		att["name"] = fmt.Sprintf("image_%d%s", index, ext)
	default:
		return nil, fmt.Errorf("unknown image kind: %s", img.Kind)
	}

	return att, nil
}

func (a *pcAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformPrivateClaw,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}

// PCSessionInfo is a summary of a session for TUI display.
type PCSessionInfo struct {
	SessionID        string
	State            string
	GroupMode        bool
	Label            string
	ParticipantCount int
	ExpiresAt        time.Time
}

func payloadKindToRole(kind string) string {
	switch kind {
	case pcKindUserMessage:
		return "user"
	case pcKindAssistantMessage:
		return "assistant"
	case pcKindSystemMessage:
		return "system"
	default:
		return kind
	}
}

// Ensure pcAdapter satisfies interfaces at compile time.
var _ startableSink = (*pcAdapter)(nil)
var _ Sink = (*pcAdapter)(nil)

// unused import guard
var _ = websocket.CloseMessage
