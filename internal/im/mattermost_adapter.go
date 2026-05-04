package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	mattermostDefaultMaxPostLen = 4000
	mattermostAPIVersion        = "api/v4"
	mattermostConnectTimeout    = 20 * time.Second
	mattermostHeartbeatPeriod   = 30 * time.Second
	mattermostDedupMaxSize      = 1000
	mattermostRequestTimeout    = 30 * time.Second
)

type mattermostAdapter struct {
	name    string
	manager *Manager
	baseURL string
	token   string

	// Bot identity (fetched after connect)
	botUserID   string
	botUsername string

	// Policies
	requireMention bool
	freeChannels   []string
	replyMode      string // "thread" or "off"
	allowedUsers   []string

	mu        sync.RWMutex
	ws        *websocket.Conn
	conn      *http.Client
	connected bool
	closed    bool
	seen      map[string]time.Time
}

func newMattermostAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*mattermostAdapter, error) {
	baseURL := strings.TrimSpace(stringValue(adapterCfg.Extra, "url"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("MATTERMOST_URL"))
	}
	if baseURL == "" {
		return nil, fmt.Errorf("Mattermost url is required for adapter %q (set 'url' in extra or MATTERMOST_URL env)", name)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	token := strings.TrimSpace(stringValue(adapterCfg.Extra, "token"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("MATTERMOST_TOKEN"))
	}
	if token == "" {
		return nil, fmt.Errorf("Mattermost token is required for adapter %q (set 'token' in extra or MATTERMOST_TOKEN env)", name)
	}

	// Mention policy
	requireMention := true
	if v := strings.ToLower(stringValue(adapterCfg.Extra, "require_mention")); v == "false" || v == "0" || v == "no" {
		requireMention = false
	}
	if envVal := os.Getenv("MATTERMOST_REQUIRE_MENTION"); envVal != "" {
		if strings.ToLower(envVal) == "false" || envVal == "0" || strings.ToLower(envVal) == "no" {
			requireMention = false
		}
	}

	freeChannels := parseCommaList(stringValue(adapterCfg.Extra, "free_channels"), os.Getenv("MATTERMOST_FREE_RESPONSE_CHANNELS"))
	allowedUsers := parseCommaList(stringValue(adapterCfg.Extra, "allowed_users"), os.Getenv("MATTERMOST_ALLOWED_USERS"))

	replyMode := strings.ToLower(strings.TrimSpace(stringValue(adapterCfg.Extra, "reply_mode")))
	if replyMode == "" {
		replyMode = strings.ToLower(strings.TrimSpace(os.Getenv("MATTERMOST_REPLY_MODE")))
	}
	if replyMode == "" {
		replyMode = "off"
	}

	return &mattermostAdapter{
		name:           name,
		manager:        mgr,
		baseURL:        baseURL,
		token:          token,
		requireMention: requireMention,
		freeChannels:   freeChannels,
		replyMode:      replyMode,
		allowedUsers:   allowedUsers,
		conn:           &http.Client{Timeout: mattermostRequestTimeout},
		seen:           make(map[string]time.Time),
	}, nil
}

func (a *mattermostAdapter) Name() string { return a.name }

func (a *mattermostAdapter) Start(ctx context.Context) {
	debug.Log("mattermost", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.mattermost.run", func() { a.run(ctx) })
}

func (a *mattermostAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.connected = false
	if a.ws != nil {
		a.ws.Close()
		a.ws = nil
	}
	return nil
}

func (a *mattermostAdapter) run(ctx context.Context) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	attempt := 0
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("mattermost", "adapter=%s error: %v", a.name, err)
		}
		a.mu.RLock()
		isClosed := a.closed
		a.mu.RUnlock()
		if isClosed {
			return
		}
		delay := backoffs[min(attempt, len(backoffs)-1)]
		attempt++
		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-time.After(delay):
		}
	}
}

func (a *mattermostAdapter) connectAndServe(ctx context.Context) error {
	// 1. Authenticate via REST to get bot identity
	me, err := a.apiGet("users/me")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	a.botUserID, _ = me["id"].(string)
	a.botUsername, _ = me["username"].(string)
	debug.Log("mattermost", "adapter=%s authenticated as @%s (%s)", a.name, a.botUsername, a.botUserID)

	// 2. Connect WebSocket
	wsURL := strings.Replace(a.baseURL, "http", "ws", 1) + "/" + mattermostAPIVersion + "/websocket"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}

	// 3. Authenticate WebSocket
	authMsg := map[string]any{
		"seq":    1,
		"action": "authentication_challenge",
		"data":   map[string]any{"token": a.token},
	}
	if err := ws.WriteJSON(authMsg); err != nil {
		ws.Close()
		return fmt.Errorf("ws auth: %w", err)
	}

	a.mu.Lock()
	a.ws = ws
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("mattermost", "adapter=%s connected to %s", a.name, wsURL)

	defer func() {
		a.mu.Lock()
		a.connected = false
		if a.ws != nil {
			a.ws.Close()
			a.ws = nil
		}
		a.mu.Unlock()
	}()

	// 4. Read loop
	for {
		if ctx.Err() != nil {
			return nil
		}
		a.mu.RLock()
		wsConn := a.ws
		a.mu.RUnlock()
		if wsConn == nil {
			return fmt.Errorf("websocket closed")
		}
		_, msgBytes, err := wsConn.ReadMessage()
		if err != nil {
			return fmt.Errorf("ws read: %w", err)
		}
		var event map[string]any
		if err := json.Unmarshal(msgBytes, &event); err != nil {
			continue
		}
		a.handleWSEvent(ctx, event)
	}
}

func (a *mattermostAdapter) handleWSEvent(ctx context.Context, event map[string]any) {
	eventType, _ := event["event"].(string)
	if eventType != "posted" {
		return
	}

	data, _ := event["data"].(map[string]any)
	if data == nil {
		return
	}

	rawPostStr, _ := data["post"].(string)
	if rawPostStr == "" {
		return
	}

	var post map[string]any
	if err := json.Unmarshal([]byte(rawPostStr), &post); err != nil {
		return
	}

	// Ignore own messages
	userID, _ := post["user_id"].(string)
	if userID == a.botUserID {
		return
	}

	// Ignore system posts (join/leave, channel created, etc.)
	// System posts have non-empty "type" field (e.g. "system_join_channel", "system_leave_channel")
	if postType, _ := post["type"].(string); postType != "" {
		return
	}

	postID, _ := post["id"].(string)
	if postID == "" {
		return
	}

	// Dedup
	a.mu.Lock()
	if _, seen := a.seen[postID]; seen {
		a.mu.Unlock()
		return
	}
	a.seen[postID] = time.Now()
	if len(a.seen) > mattermostDedupMaxSize {
		cutoff := time.Now().Add(-5 * time.Minute)
		for k, t := range a.seen {
			if t.Before(cutoff) {
				delete(a.seen, k)
			}
		}
	}
	a.mu.Unlock()

	channelID, _ := post["channel_id"].(string)
	channelTypeRaw, _ := data["channel_type"].(string)
	messageText, _ := post["message"].(string)
	senderName, _ := data["sender_name"].(string)
	senderName = strings.TrimPrefix(senderName, "@")

	// Determine if DM
	isDM := channelTypeRaw == "D"

	// Allowed users check
	if len(a.allowedUsers) > 0 && !entryMatches(a.allowedUsers, userID) {
		debug.Log("mattermost", "adapter=%s user %s not in allowed list", a.name, userID)
		return
	}

	// Mention gating for non-DM channels
	if !isDM {
		isFree := entryMatches(a.freeChannels, channelID)
		if !isFree && a.requireMention {
			hasMention := a.hasMention(messageText)
			if !hasMention {
				return
			}
			messageText = a.stripMention(messageText)
		}
	}

	if strings.TrimSpace(messageText) == "" {
		return
	}

	// Thread support
	threadID, _ := post["root_id"].(string)

	// Extract file IDs as attachment metadata
	fileIDs, _ := post["file_ids"].([]any)
	var attachments []Attachment
	for _, fid := range fileIDs {
		idStr, _ := fid.(string)
		if idStr != "" {
			attachments = append(attachments, Attachment{
				Kind: AttachmentFile,
				Name: idStr, // Will be resolved to URL by downstream if needed
				URL:  fmt.Sprintf("%s/%s/files/%s", a.baseURL, mattermostAPIVersion, idStr),
			})
		}
	}

	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformMattermost,
			ChannelID:  channelID,
			ThreadID:   threadID,
			SenderID:   userID,
			SenderName: senderName,
			MessageID:  postID,
			ReceivedAt: time.Now(),
		},
		Text:        strings.TrimSpace(messageText),
		Attachments: attachments,
	}

	// Pairing flow
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("mattermost", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err != ErrNoSessionBound {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendText(ctx, channelID, threadID, pairingResult.ReplyText)
			if pairingResult.Bound && pairingResult.PreviousBinding != nil {
				if err := a.manager.SendDirect(ctx, *pairingResult.PreviousBinding, OutboundEvent{
					Kind: OutboundEventText,
					Text: "当前目录已绑定到其他渠道，如需重新绑定请再次发起配对。",
				}); err != nil {
					debug.Log("mattermost", "adapter=%s notify previous: %v", a.name, err)
				}
			}
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, msg)
	}
}

func (a *mattermostAdapter) hasMention(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "@"+strings.ToLower(a.botUsername)) {
		return true
	}
	if strings.Contains(lower, "@"+strings.ToLower(a.botUserID)) {
		return true
	}
	return false
}

var mentionRegex = regexp.MustCompile(`(?i)@\S+`)

func (a *mattermostAdapter) stripMention(text string) string {
	// Strip @bot_username and @bot_user_id mentions
	for _, pattern := range []string{a.botUsername, a.botUserID} {
		re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(pattern))
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}

// --- Outbound ---

func (a *mattermostAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	chatID := binding.ChannelID
	if chatID == "" {
		chatID = binding.TargetID
	}
	return a.sendText(ctx, chatID, binding.ThreadID, event.Text)
}

func (a *mattermostAdapter) sendText(ctx context.Context, channelID, rootID, text string) error {
	if text == "" || channelID == "" {
		return nil
	}
	if len(text) > mattermostDefaultMaxPostLen {
		text = text[:mattermostDefaultMaxPostLen]
	}

	payload := map[string]any{
		"channel_id": channelID,
		"message":    text,
	}
	if rootID != "" && a.replyMode == "thread" {
		payload["root_id"] = rootID
	}

	_, err := a.apiPost("posts", payload)
	if err != nil {
		return fmt.Errorf("Mattermost send: %w", err)
	}
	return nil
}

// --- REST API helpers ---

func (a *mattermostAdapter) apiURL(path string) string {
	return a.baseURL + "/" + mattermostAPIVersion + "/" + strings.TrimPrefix(path, "/")
}

func (a *mattermostAdapter) apiGet(path string) (map[string]any, error) {
	req, err := http.NewRequest("GET", a.apiURL(path), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.conn.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %s → %d: %s", path, resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("GET %s decode: %w", path, err)
	}
	return result, nil
}

func (a *mattermostAdapter) apiPost(path string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", a.apiURL(path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.conn.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("POST %s → %d: %s", path, resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("POST %s decode: %w", path, err)
	}
	return result, nil
}

func (a *mattermostAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformMattermost,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}

// --- Helpers ---

func parseCommaList(values ...string) []string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			var result []string
			for _, part := range strings.Split(v, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					result = append(result, part)
				}
			}
			return result
		}
	}
	return nil
}
