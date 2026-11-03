package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	imagepkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	mattermostDefaultMaxPostLen = 16383
	mattermostAPIVersion        = "api/v4"
	mattermostHealthySession    = 60 * time.Second
	mattermostConnectTimeout    = 20 * time.Second
	mattermostHeartbeatPeriod   = 30 * time.Second
	mattermostDedupMaxSize      = 1000
	mattermostRequestTimeout    = 30 * time.Second
	mattermostInterMsgDelay     = 200 * time.Millisecond
)

var mattermostWSReadTimeout = mattermostHeartbeatPeriod + 30*time.Second

type mattermostAdapter struct {
	name    string
	manager *Manager
	baseURL string
	token   string

	botUserID   string
	botUsername string
	teamName    string

	requireMention bool
	freeChannels   []string
	replyMode      string
	allowedUsers   []string

	mu          sync.RWMutex
	ws          *websocket.Conn
	writeMu     sync.Mutex
	conn        *http.Client
	connected   bool
	closed      bool
	healthyAt   time.Time
	seen        map[string]time.Time
	reactionAck reactionAckState
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
		conn:           util.NewInsecureAwareClient(mattermostRequestTimeout),
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
	a.closed = true
	a.connected = false
	ws := a.ws
	a.ws = nil
	a.mu.Unlock()

	if ws != nil {
		ws.Close()
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
		err := a.connectAndServe(ctx)
		if err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("mattermost", "adapter=%s error: %v", a.name, err)
		}

		if !a.healthyAt.IsZero() && time.Since(a.healthyAt) >= mattermostHealthySession {
			attempt = 0
		}
		a.healthyAt = time.Time{}
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
		case <-time.After(jitterDuration(delay)):
		}
	}
}

func (a *mattermostAdapter) connectAndServe(ctx context.Context) error {

	me, err := a.apiGet("users/me")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	a.mu.Lock()
	a.botUserID, _ = me["id"].(string)
	a.botUsername, _ = me["username"].(string)
	a.mu.Unlock()
	debug.Log("mattermost", "adapter=%s authenticated as @%s (%s)", a.name, a.botUsername, a.botUserID)

	a.fetchTeamInfo(ctx)

	wsURL := strings.Replace(a.baseURL, "http", "ws", 1) + "/" + mattermostAPIVersion + "/websocket"
	dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dialCancel()
	ws, _, err := websocket.DefaultDialer.DialContext(dialCtx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}

	authMsg := map[string]any{
		"seq":    1,
		"action": "authentication_challenge",
		"data":   map[string]any{"token": a.token},
	}
	if err := ws.WriteJSON(authMsg); err != nil {
		ws.Close()
		return fmt.Errorf("ws auth: %w", err)
	}

	ws.SetReadDeadline(time.Now().Add(mattermostWSReadTimeout))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(mattermostWSReadTimeout))
		return nil
	})

	a.mu.Lock()
	a.ws = ws
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	a.healthyAt = time.Now()
	debug.Log("mattermost", "adapter=%s connected to %s", a.name, wsURL)

	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()
	safego.Go("im.mattermost.heartbeat", func() {
		ticker := time.NewTicker(mattermostHeartbeatPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				a.mu.RLock()
				wsConn := a.ws
				a.mu.RUnlock()
				if wsConn == nil {
					return
				}
				pingMsg := map[string]any{
					"seq":    0,
					"action": "ping",
				}
				a.writeMu.Lock()
				err := wsConn.WriteJSON(pingMsg)
				a.writeMu.Unlock()
				if err != nil {
					debug.Log("mattermost", "adapter=%s heartbeat ping failed: %v", a.name, err)
					return
				}
			}
		}
	})

	defer func() {
		a.mu.Lock()
		a.connected = false
		if a.ws != nil {
			a.ws.Close()
			a.ws = nil
		}
		a.mu.Unlock()
	}()

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

		wsConn.SetReadDeadline(time.Now().Add(mattermostWSReadTimeout))
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

	userID, _ := post["user_id"].(string)
	if userID == a.botUserID {
		return
	}

	if postType, _ := post["type"].(string); postType != "" {
		return
	}

	postID, _ := post["id"].(string)
	if postID == "" {
		return
	}

	a.mu.Lock()
	if _, seen := a.seen[postID]; seen {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	channelID, _ := post["channel_id"].(string)
	channelTypeRaw, _ := data["channel_type"].(string)
	messageText, _ := post["message"].(string)
	senderName, _ := data["sender_name"].(string)
	senderName = strings.TrimPrefix(senderName, "@")

	isDM := channelTypeRaw == "D"

	if len(a.allowedUsers) > 0 && !entryMatches(a.allowedUsers, userID) {
		debug.Log("mattermost", "adapter=%s user %s not in allowed list", a.name, userID)
		return
	}

	if !isDM {
		isFree := entryMatches(a.freeChannels, channelID)
		if !isFree && a.requireMention {
			hasMention := a.hasMention(messageText)
			if !hasMention {

				debug.Log("mattermost", "adapter=%s channel %s message without mention dropped", a.name, channelID)
				return
			}
			messageText = a.stripMention(messageText)
		}
	}

	if strings.TrimSpace(messageText) == "" {
		return
	}

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

		for len(a.seen) > mattermostDedupMaxSize {
			oldestKey := ""
			var oldestTime time.Time
			for k, t := range a.seen {
				if oldestKey == "" || t.Before(oldestTime) {
					oldestKey, oldestTime = k, t
				}
			}
			delete(a.seen, oldestKey)
		}
	}
	a.mu.Unlock()

	threadID, _ := post["root_id"].(string)

	fileIDs, _ := post["file_ids"].([]any)
	var attachments []Attachment
	for _, fid := range fileIDs {
		idStr, _ := fid.(string)
		if idStr != "" {
			attachments = append(attachments, Attachment{
				Kind: AttachmentFile,
				Name: idStr,
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

	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("mattermost", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err != ErrNoSessionBound {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendText(ctx, channelID, threadID, pairingResult.ReplyText)
			if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
				debug.Log("mattermost", "adapter=%s notify previous: %v", a.name, err)
			}
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, msg)
	}
}

const mattermostUsernameContChars = "abcdefghijklmnopqrstuvwxyz0123456789._-"

func mattermostIsUsernameContByte(b byte) bool {
	return strings.IndexByte(mattermostUsernameContChars, b) >= 0
}

func mentionsMattermostHandle(lower, handle string) bool {
	needle := "@" + strings.ToLower(handle)
	for start := 0; ; {
		i := strings.Index(lower[start:], needle)
		if i < 0 {
			return false
		}
		at := start + i
		end := at + len(needle)
		if end >= len(lower) || !mattermostIsUsernameContByte(lower[end]) {
			return true
		}
		start = at + 1
	}
}

func stripMattermostHandle(text, handle string) string {
	needle := "@" + strings.ToLower(handle)
	var b strings.Builder
	for i := 0; i < len(text); {
		if n, ok := matrixASCIIFoldPrefix(text[i:], needle); ok {
			end := i + n
			if end >= len(text) || !mattermostIsUsernameContByte(text[end]) {
				i = end
				continue
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

func (a *mattermostAdapter) hasMention(text string) bool {
	lower := strings.ToLower(text)

	for _, id := range []string{a.botUsername, a.botUserID} {
		if id == "" {
			continue
		}
		if mentionsMattermostHandle(lower, id) {
			return true
		}
	}
	return false
}

func (a *mattermostAdapter) stripMention(text string) string {

	for _, handle := range []string{a.botUsername, a.botUserID} {
		if handle == "" {
			continue
		}
		text = stripMattermostHandle(text, handle)
	}
	return strings.TrimSpace(text)
}

func (a *mattermostAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	chatID := binding.ChannelID
	if chatID == "" {
		chatID = binding.TargetID
	}
	text := strings.TrimSpace(defaultOutboundText(event))
	if text == "" {
		return nil
	}

	images, remainingText := ExtractImagesFromText(text)
	var fileIDs []string
	for i, img := range images {
		data, mimeType, err := a.resolveImageToBytes(ctx, img)
		if err != nil {
			debug.Log("mattermost", "adapter=%s image resolve failed [%d/%d]: %v", a.name, i+1, len(images), err)
			continue
		}
		fileID, err := a.uploadFile(ctx, chatID, data, mimeType)
		if err != nil {
			debug.Log("mattermost", "adapter=%s image upload failed [%d/%d]: %v", a.name, i+1, len(images), err)
			continue
		}
		fileIDs = append(fileIDs, fileID)
		debug.Log("mattermost", "adapter=%s image uploaded [%d/%d] mime=%s size=%d file_id=%s", a.name, i+1, len(images), mimeType, len(data), fileID)

		if i < len(images)-1 {
			select {
			case <-time.After(mattermostInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	if remainingText == "" && len(fileIDs) == 0 {
		if len(images) > 0 {

			remainingText = text
		} else {
			return nil
		}
	}
	return a.sendTextWithFiles(ctx, chatID, binding.ThreadID, remainingText, fileIDs)
}

func (a *mattermostAdapter) sendText(ctx context.Context, channelID, rootID, text string) error {
	return a.sendTextWithFiles(ctx, channelID, rootID, text, nil)
}

func (a *mattermostAdapter) sendTextWithFiles(ctx context.Context, channelID, rootID, text string, fileIDs []string) error {
	if text == "" && len(fileIDs) == 0 {
		return nil
	}
	if channelID == "" {
		return nil
	}

	chunks := SplitMarkdown(text, mattermostDefaultMaxPostLen)
	if len(chunks) == 0 && len(fileIDs) > 0 {

		chunks = []string{""}
	}
	for i, chunk := range chunks {
		payload := map[string]any{
			"channel_id": channelID,
			"message":    chunk,
		}

		if rootID != "" && a.replyMode == "thread" {
			payload["root_id"] = rootID
		}

		if i == 0 && len(fileIDs) > 0 {
			payload["file_ids"] = fileIDs
		}
		result, err := a.apiPostCtx(ctx, "posts", payload)
		if err != nil {
			return fmt.Errorf("Mattermost send chunk %d/%d: %w", i+1, len(chunks), err)
		}

		if rootID == "" && len(chunks) > 1 && i == 0 {
			if newID, ok := result["id"].(string); ok && newID != "" {
				rootID = newID
			}
		}

		if i < len(chunks)-1 {
			select {
			case <-time.After(mattermostInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

func (a *mattermostAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return nil
	}
	postID := strings.TrimSpace(LastReactionTargetMessageID(binding))
	if postID != "" {
		if !a.reactionAck.NeedsSend(binding, postID) {
			return nil
		}
		a.mu.RLock()
		botUserID := strings.TrimSpace(a.botUserID)
		conn := a.conn
		a.mu.RUnlock()
		if botUserID != "" && conn != nil {
			_, err := a.apiPost("reactions", map[string]any{
				"user_id":    botUserID,
				"post_id":    postID,
				"emoji_name": reactionAckValue(PlatformMattermost, postID),
			})
			if err != nil {
				debug.Log("mattermost", "adapter=%s typing reaction failed: %v", a.name, err)
				return err
			}
			a.reactionAck.MarkSent(binding, postID)
			return nil
		}
	}
	return a.triggerNativeTyping(channelID)
}

func (a *mattermostAdapter) triggerNativeTyping(channelID string) error {
	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return nil
	}
	typingMsg := map[string]any{
		"seq":    0,
		"action": "user_typing",
		"data": map[string]any{
			"channel_id": channelID,
		},
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return ws.WriteJSON(typingMsg)
}

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
	return a.apiPostCtx(context.Background(), path, payload)
}

func (a *mattermostAdapter) apiPostCtx(ctx context.Context, path string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL(path), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+a.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := a.conn.Do(req)
		if err != nil {
			return nil, fmt.Errorf("POST %s: %w", path, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt < maxRateLimitRetries {
				delay := parseRetryAfter(resp)
				debug.Log("mattermost", "adapter=%s POST %s 429 rate limited, retry %d/%d in %v",
					a.name, path, attempt+1, maxRateLimitRetries, delay)
				if err := sleepRetry(ctx, delay); err != nil {
					return nil, err
				}
				continue
			}
			return nil, rateLimitExhausted("Mattermost")
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
	return nil, rateLimitExhausted("Mattermost")
}

func (a *mattermostAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	contactURI := ""
	if a.teamName != "" && a.botUsername != "" {
		contactURI = a.baseURL + "/" + a.teamName + "/messages/@" + a.botUsername
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformMattermost,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		UpdatedAt:  time.Now(),
	})
}

func (a *mattermostAdapter) fetchTeamInfo(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiURL("users/me/teams"), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.conn.Do(req)
	if err != nil {
		debug.Log("mattermost", "adapter=%s fetch teams: %v", a.name, err)
		return
	}
	defer resp.Body.Close()
	var teams []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&teams); err != nil {
		debug.Log("mattermost", "adapter=%s parse teams: %v", a.name, err)
		return
	}
	for _, t := range teams {
		if slug, ok := t["name"].(string); ok && slug != "" {
			a.mu.Lock()
			a.teamName = slug
			a.mu.Unlock()
			debug.Log("mattermost", "adapter=%s team=%s", a.name, slug)
			return
		}
	}
}

func (a *mattermostAdapter) resolveImageToBytes(ctx context.Context, img ExtractedImage) ([]byte, string, error) {
	switch img.Kind {
	case "data":
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

		if IsLocalFilePath(img.Data) {

			f, err := os.Open(img.Data)
			if err != nil {
				return nil, "", fmt.Errorf("read local image: %w", err)
			}
			data, err := imagepkg.ReadLimited(f, imagepkg.MaxSize)
			f.Close()
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

func (a *mattermostAdapter) uploadFile(ctx context.Context, channelID string, data []byte, mimeType string) (string, error) {

	ext := ".png"
	switch mimeType {
	case "image/jpeg", "image/jpg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename="image%s"`, ext))
	h.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return "", fmt.Errorf("create multipart field: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("write image data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	url := a.apiURL(fmt.Sprintf("files?channel_id=%s", channelID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", fmt.Errorf("create upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.conn.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("upload file → %d: %s", resp.StatusCode, string(respBody))
	}

	var fileInfos []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&fileInfos); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	if len(fileInfos) == 0 {
		return "", fmt.Errorf("upload returned no file infos")
	}
	fileID, ok := fileInfos[0]["id"].(string)
	if !ok || fileID == "" {
		return "", fmt.Errorf("upload response missing file ID")
	}
	return fileID, nil
}

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
