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
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	matrixMaxMessageLen   = 4000
	matrixSyncTimeout     = 30 // seconds for long-poll
	matrixConnectTimeout  = 20 * time.Second
	matrixRequestTimeout  = 30 * time.Second
	matrixDedupMaxSize    = 1000
	matrixTypingTimeoutMs = 30000
	matrixBackoffMax      = 60 * time.Second
	matrixInitialBackoff  = 2 * time.Second
)

type matrixAdapter struct {
	name       string
	manager    *Manager
	homeserver string
	token      string

	// Bot identity (resolved after whoami)
	userID   string
	deviceID string

	// Policies
	requireMention bool
	freeRooms      []string
	allowedUsers   []string

	mu        sync.RWMutex
	conn      *http.Client
	connected bool
	closed    bool

	// Sync state
	nextBatch atomic.Value // string

	// DM room cache: room_id → true
	dmRooms map[string]bool

	// Dedup
	seen map[string]time.Time

	// Transaction ID counter for send
	txnID atomic.Int64
}

func newMatrixAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*matrixAdapter, error) {
	homeserver := strings.TrimSpace(stringValue(adapterCfg.Extra, "homeserver"))
	if homeserver == "" {
		homeserver = strings.TrimSpace(os.Getenv("MATRIX_HOMESERVER"))
	}
	if homeserver == "" {
		return nil, fmt.Errorf("Matrix homeserver is required for adapter %q (set 'homeserver' in extra or MATRIX_HOMESERVER env)", name)
	}
	homeserver = strings.TrimRight(homeserver, "/")

	token := strings.TrimSpace(stringValue(adapterCfg.Extra, "access_token"))
	if token == "" {
		token = strings.TrimSpace(stringValue(adapterCfg.Extra, "token"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("MATRIX_ACCESS_TOKEN"))
	}
	if token == "" {
		return nil, fmt.Errorf("Matrix access_token is required for adapter %q (set 'access_token' in extra or MATRIX_ACCESS_TOKEN env)", name)
	}

	userID := strings.TrimSpace(stringValue(adapterCfg.Extra, "user_id"))
	if userID == "" {
		userID = strings.TrimSpace(os.Getenv("MATRIX_USER_ID"))
	}

	// Mention policy
	requireMention := true
	if v := strings.ToLower(stringValue(adapterCfg.Extra, "require_mention")); v == "false" || v == "0" || v == "no" {
		requireMention = false
	}
	if envVal := os.Getenv("MATRIX_REQUIRE_MENTION"); envVal != "" {
		if strings.ToLower(envVal) == "false" || envVal == "0" || strings.ToLower(envVal) == "no" {
			requireMention = false
		}
	}

	freeRooms := parseCommaList(stringValue(adapterCfg.Extra, "free_rooms"), os.Getenv("MATRIX_FREE_ROOMS"))
	allowedUsers := parseCommaList(stringValue(adapterCfg.Extra, "allowed_users"), os.Getenv("MATRIX_ALLOWED_USERS"))

	return &matrixAdapter{
		name:           name,
		manager:        mgr,
		homeserver:     homeserver,
		token:          token,
		userID:         userID,
		requireMention: requireMention,
		freeRooms:      freeRooms,
		allowedUsers:   allowedUsers,
		conn:           &http.Client{Timeout: matrixRequestTimeout},
		dmRooms:        make(map[string]bool),
		seen:           make(map[string]time.Time),
	}, nil
}

func (a *matrixAdapter) Name() string { return a.name }

func (a *matrixAdapter) Start(ctx context.Context) {
	debug.Log("matrix", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.matrix.run", func() { a.run(ctx) })
}

func (a *matrixAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.connected = false
	return nil
}

func (a *matrixAdapter) run(ctx context.Context) {
	backoff := matrixInitialBackoff
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("matrix", "adapter=%s error: %v", a.name, err)
		}
		a.mu.RLock()
		isClosed := a.closed
		a.mu.RUnlock()
		if isClosed {
			return
		}
		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-time.After(backoff):
		}
		if backoff < matrixBackoffMax {
			backoff *= 2
			if backoff > matrixBackoffMax {
				backoff = matrixBackoffMax
			}
		}
	}
}

func (a *matrixAdapter) connectAndServe(ctx context.Context) error {
	// 1. Authenticate via whoami
	me, err := a.apiGet("_matrix/client/v3/account/whoami")
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	if resolvedID, ok := me["user_id"].(string); ok && resolvedID != "" {
		a.userID = resolvedID
	}
	if devID, ok := me["device_id"].(string); ok {
		a.deviceID = devID
	}
	debug.Log("matrix", "adapter=%s authenticated as %s (device %s)", a.name, a.userID, a.deviceID)

	// 2. Initial sync (full, no timeout)
	syncURL := "_matrix/client/v3/sync?timeout=0"
	if nb := a.nextBatch.Load(); nb != nil && nb.(string) != "" {
		// Already have a sync token, do incremental
	} else {
		syncData, err := a.apiGetRaw(syncURL)
		if err != nil {
			return fmt.Errorf("initial sync: %w", err)
		}
		a.processSyncResponse(syncData)
	}

	// 3. Fetch DM rooms
	a.fetchDMRooms()

	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("matrix", "adapter=%s connected to %s", a.name, a.homeserver)

	defer func() {
		a.mu.Lock()
		a.connected = false
		a.mu.Unlock()
	}()

	// 4. Sync loop (long-poll)
	debug.Log("matrix", "adapter=%s entering sync loop", a.name)
	backoff := matrixInitialBackoff
	for {
		if ctx.Err() != nil {
			debug.Log("matrix", "adapter=%s sync loop: context done", a.name)
			return nil
		}
		a.mu.RLock()
		isClosed := a.closed
		a.mu.RUnlock()
		if isClosed {
			return nil
		}

		nb := ""
		if v := a.nextBatch.Load(); v != nil {
			nb = v.(string)
		}
		url := fmt.Sprintf("_matrix/client/v3/sync?since=%s&timeout=%d", nb, matrixSyncTimeout)
		syncData, err := a.apiGetRaw(url)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") ||
				strings.Contains(errStr, "unauthorized") || strings.Contains(errStr, "forbidden") {
				return fmt.Errorf("permanent auth error: %w", err)
			}
			debug.Log("matrix", "adapter=%s sync error: %v, retrying in %v", a.name, err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < matrixBackoffMax {
				backoff *= 2
			}
			continue
		}
		backoff = matrixInitialBackoff // reset on success
		a.processSyncResponse(syncData)
		a.dispatchSyncEvents(ctx, syncData)
	}
}

func (a *matrixAdapter) processSyncResponse(data map[string]any) {
	if nb, ok := data["next_batch"].(string); ok && nb != "" {
		a.nextBatch.Store(nb)
	}
}

func (a *matrixAdapter) dispatchSyncEvents(ctx context.Context, data map[string]any) {
	rooms, _ := data["rooms"].(map[string]any)
	if rooms == nil {
		return
	}

	// Auto-join invited rooms
	invited, _ := rooms["invite"].(map[string]any)
	for roomID := range invited {
		debug.Log("matrix", "adapter=%s invited to room=%s, auto-joining", a.name, roomID)
		if err := a.joinRoom(roomID); err != nil {
			debug.Log("matrix", "adapter=%s failed to join room=%s: %v", a.name, roomID, err)
		}
	}

	joined, _ := rooms["join"].(map[string]any)
	if joined == nil {
		return
	}
	if len(joined) > 0 {
		debug.Log("matrix", "adapter=%s sync: %d joined room(s)", a.name, len(joined))
	}
	for roomID, roomData := range joined {
		roomMap, ok := roomData.(map[string]any)
		if !ok {
			continue
		}

		// Ensure DM status is known before processing messages
		a.ensureDMStatus(roomID, roomMap)

		timeline, _ := roomMap["timeline"].(map[string]any)
		if timeline == nil {
			continue
		}
		events, _ := timeline["events"].([]any)
		if len(events) > 0 {
			debug.Log("matrix", "adapter=%s room=%s: %d timeline event(s)", a.name, roomID, len(events))
		}
		for _, ev := range events {
			event, ok := ev.(map[string]any)
			if !ok {
				continue
			}
			et, _ := event["type"].(string)
			debug.Log("matrix", "adapter=%s dispatching event type=%s room=%s", a.name, et, roomID)
			a.handleRoomEvent(ctx, roomID, event)
		}
	}
}

func (a *matrixAdapter) handleRoomEvent(ctx context.Context, roomID string, event map[string]any) {
	eventType, _ := event["type"].(string)
	if eventType != "m.room.message" {
		return
	}

	sender, _ := event["sender"].(string)
	debug.Log("matrix", "adapter=%s handleRoomEvent room=%s sender=%s self=%s", a.name, roomID, sender, a.userID)
	if sender == a.userID {
		return
	}

	content, _ := event["content"].(map[string]any)
	if content == nil {
		return
	}

	// Skip m.notice (bot-to-bot loop prevention)
	msgtype, _ := content["msgtype"].(string)
	if msgtype == "m.notice" {
		return
	}

	// Skip edits (m.replace relation)
	relatesTo, _ := content["m.relates_to"].(map[string]any)
	if relatesTo != nil {
		if relType, _ := relatesTo["rel_type"].(string); relType == "m.replace" {
			return
		}
	}

	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		return
	}

	// Dedup
	a.mu.Lock()
	if _, seen := a.seen[eventID]; seen {
		a.mu.Unlock()
		return
	}
	a.seen[eventID] = time.Now()
	if len(a.seen) > matrixDedupMaxSize {
		cutoff := time.Now().Add(-5 * time.Minute)
		for k, t := range a.seen {
			if t.Before(cutoff) {
				delete(a.seen, k)
			}
		}
	}
	a.mu.Unlock()

	// Extract text
	body, _ := content["body"].(string)
	if body == "" {
		return
	}

	// Only handle m.text for v1
	if msgtype != "m.text" && msgtype != "" {
		// Skip m.image, m.file, m.audio, m.video for v1
		return
	}

	// Strip reply fallback (> lines)
	if replyTo, _ := relatesTo["m.in_reply_to"].(map[string]any); replyTo != nil {
		body = stripMatrixReplyFallback(body)
	}

	// Thread ID
	threadID := ""
	if relatesTo != nil {
		if relType, _ := relatesTo["rel_type"].(string); relType == "m.thread" {
			threadID, _ = relatesTo["event_id"].(string)
		}
	}

	// DM detection
	a.mu.RLock()
	isDM := a.dmRooms[roomID]
	dmCount := len(a.dmRooms)
	a.mu.RUnlock()
	if !isDM {
		// Inline fallback: query room members via API if not cached.
		// This covers rooms that appeared in incremental sync without state events.
		isDM = a.checkIsDMViaAPI(roomID)
	}
	debug.Log("matrix", "adapter=%s event room=%s sender=%s isDM=%v dmRooms=%d type=%s body=%.80s", a.name, roomID, sender, isDM, dmCount, msgtype, body)

	// Allowed users check
	if len(a.allowedUsers) > 0 && !entryMatches(a.allowedUsers, sender) {
		debug.Log("matrix", "adapter=%s user %s not in allowed list", a.name, sender)
		return
	}

	// Mention gating for non-DM rooms
	if !isDM {
		isFree := entryMatches(a.freeRooms, roomID)
		if !isFree && a.requireMention {
			hasMention := a.hasMention(body, content)
			debug.Log("matrix", "adapter=%s non-DM room=%s free=%v mention=%v requireMention=%v — dropping", a.name, roomID, isFree, hasMention, a.requireMention)
			if !hasMention {
				return
			}
			body = a.stripMention(body)
		}
	}

	if strings.TrimSpace(body) == "" {
		return
	}

	// Extract sender display name
	senderName := sender
	if profile, _ := event["sender_profile"].(map[string]any); profile != nil {
		if dn, ok := profile["displayname"].(string); ok && dn != "" {
			senderName = dn
		}
	}

	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformMatrix,
			ChannelID:  roomID,
			ThreadID:   threadID,
			SenderID:   sender,
			SenderName: senderName,
			MessageID:  eventID,
			ReceivedAt: time.Now(),
		},
		Text: strings.TrimSpace(body),
	}

	// Pairing flow
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("matrix", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err != ErrNoSessionBound {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendText(ctx, roomID, threadID, pairingResult.ReplyText)
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, msg)
	}
}

func (a *matrixAdapter) hasMention(body string, content map[string]any) bool {
	lower := strings.ToLower(body)

	// Check m.mentions.user_ids (MSC3952)
	if mentions, _ := content["m.mentions"].(map[string]any); mentions != nil {
		if uids, _ := mentions["user_ids"].([]any); uids != nil {
			for _, uid := range uids {
				if uid == a.userID {
					return true
				}
			}
		}
	}

	// Fallback: @userid in body
	if strings.Contains(lower, strings.ToLower(a.userID)) {
		return true
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

var matrixMentionRegex = regexp.MustCompile(`(?i)@\S+`)

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

func stripMatrixReplyFallback(body string) string {
	if !strings.HasPrefix(body, "> ") && !strings.HasPrefix(body, ">") {
		return body
	}
	lines := strings.Split(body, "\n")
	var stripped []string
	pastFallback := false
	for _, line := range lines {
		if !pastFallback {
			if strings.HasPrefix(line, "> ") || line == ">" {
				continue
			}
			if line == "" {
				pastFallback = true
				continue
			}
			pastFallback = true
		}
		stripped = append(stripped, line)
	}
	if len(stripped) == 0 {
		return body
	}
	return strings.Join(stripped, "\n")
}

// --- Outbound ---

func (a *matrixAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	chatID := binding.ChannelID
	if chatID == "" {
		chatID = binding.TargetID
	}
	return a.sendText(ctx, chatID, binding.ThreadID, event.Text)
}

func (a *matrixAdapter) sendText(ctx context.Context, roomID, threadID, text string) error {
	if text == "" || roomID == "" {
		return nil
	}

	// Split into chunks if needed
	chunks := splitMatrixMessage(text, matrixMaxMessageLen)
	var lastErr error
	for _, chunk := range chunks {
		msgContent := map[string]any{
			"msgtype": "m.text",
			"body":    chunk,
		}

		relatesTo := map[string]any{}
		if threadID != "" {
			relatesTo["rel_type"] = "m.thread"
			relatesTo["event_id"] = threadID
			relatesTo["is_falling_back"] = true
		}
		if len(relatesTo) > 0 {
			msgContent["m.relates_to"] = relatesTo
		}

		txnID := fmt.Sprintf("ggcode_%d", a.txnID.Add(1))
		path := fmt.Sprintf("_matrix/client/v3/rooms/%s/send/m.room.message/%s", roomID, txnID)
		_, err := a.apiPut(path, msgContent)
		if err != nil {
			lastErr = fmt.Errorf("Matrix send: %w", err)
			debug.Log("matrix", "adapter=%s send error to %s: %v", a.name, roomID, err)
		}
	}
	return lastErr
}

func splitMatrixMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		// Try to split at newline
		splitAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			splitAt = idx + 1
		}
		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}
	return chunks
}

// TriggerTyping sends a Matrix typing indicator.
func (a *matrixAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	roomID := strings.TrimSpace(binding.ChannelID)
	if roomID == "" || a.userID == "" {
		return nil
	}
	path := fmt.Sprintf("_matrix/client/v3/rooms/%s/typing/%s", roomID, a.userID)
	_, err := a.apiPut(path, map[string]any{
		"typing":  true,
		"timeout": matrixTypingTimeoutMs,
	})
	return err
}

// --- DM room detection ---

func (a *matrixAdapter) fetchDMRooms() {
	data, err := a.apiGet("_matrix/client/v3/user/" + a.userID + "/account_data/m.direct")
	if err != nil {
		debug.Log("matrix", "adapter=%s fetch DM rooms: %v", a.name, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// m.direct is { "@user:domain": ["!roomid:domain", ...], ... }
	for _, roomsVal := range data {
		if rooms, ok := roomsVal.([]any); ok {
			for _, r := range rooms {
				if roomID, ok := r.(string); ok && roomID != "" {
					a.dmRooms[roomID] = true
				}
			}
		}
	}
	debug.Log("matrix", "adapter=%s found %d DM rooms", a.name, len(a.dmRooms))
}

// ensureDMStatus checks if a room is a DM. First consults the cached m.direct
// data. If not found, falls back to counting joined members — rooms with
// exactly 2 members are treated as DMs (matching OpenClaw's 2-member fallback).
func (a *matrixAdapter) ensureDMStatus(roomID string, roomMap map[string]any) {
	a.mu.RLock()
	if a.dmRooms[roomID] {
		a.mu.RUnlock()
		return
	}
	a.mu.RUnlock()

	// Check joined member count from both state events and timeline events.
	// Incremental syncs may only include member changes in timeline.
	members := a.collectJoinedMembers(roomMap)

	// Also check timeline events for member joins
	timeline, _ := roomMap["timeline"].(map[string]any)
	if timeline != nil {
		events, _ := timeline["events"].([]any)
		for _, ev := range events {
			event, ok := ev.(map[string]any)
			if !ok {
				continue
			}
			if eventType, _ := event["type"].(string); eventType == "m.room.member" {
				content, _ := event["content"].(map[string]any)
				membership, _ := content["membership"].(string)
				userID, _ := event["state_key"].(string)
				if membership == "join" && userID != "" {
					found := false
					for _, m := range members {
						if m == userID {
							found = true
							break
						}
					}
					if !found {
						members = append(members, userID)
					}
				}
			}
		}
	}

	debug.Log("matrix", "adapter=%s room=%s ensureDMStatus: %d member(s) (%v)", a.name, roomID, len(members), members)
	if len(members) == 2 {
		a.mu.Lock()
		a.dmRooms[roomID] = true
		a.mu.Unlock()
		debug.Log("matrix", "adapter=%s room=%s detected as DM via 2-member fallback", a.name, roomID)
	}
}

// checkIsDMViaAPI queries the room membership via Matrix API and returns true
// if the room has exactly 2 joined members. Results are cached in dmRooms.
func (a *matrixAdapter) checkIsDMViaAPI(roomID string) bool {
	// Use /_matrix/client/v3/rooms/{roomId}/members with membership=join
	data, err := a.apiGet("_matrix/client/v3/rooms/" + roomID + "/members?membership=join&not_membership=leave&not_membership=ban")
	if err != nil {
		debug.Log("matrix", "adapter=%s checkIsDMViaAPI room=%s error: %v", a.name, roomID, err)
		return false
	}
	chunk, _ := data["chunk"].([]any)
	if len(chunk) == 2 {
		a.mu.Lock()
		a.dmRooms[roomID] = true
		a.mu.Unlock()
		debug.Log("matrix", "adapter=%s room=%s detected as DM via API member query (2 members)", a.name, roomID)
		return true
	}
	debug.Log("matrix", "adapter=%s room=%s API member query: %d members", a.name, roomID, len(chunk))
	return false
}

// collectJoinedMembers extracts joined member user IDs from room state events.
func (a *matrixAdapter) collectJoinedMembers(roomMap map[string]any) []string {
	state, _ := roomMap["state"].(map[string]any)
	if state == nil {
		return nil
	}
	events, _ := state["events"].([]any)
	seen := make(map[string]bool)
	for _, ev := range events {
		event, ok := ev.(map[string]any)
		if !ok {
			continue
		}
		if eventType, _ := event["type"].(string); eventType != "m.room.member" {
			continue
		}
		content, _ := event["content"].(map[string]any)
		membership, _ := content["membership"].(string)
		if membership != "join" {
			continue
		}
		userID, _ := event["state_key"].(string)
		if userID != "" {
			seen[userID] = true
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	return result
}

// joinRoom sends a join request for the given room.
func (a *matrixAdapter) joinRoom(roomID string) error {
	_, err := a.apiPost("_matrix/client/v3/join/"+roomID, nil)
	return err
}

// --- REST API helpers ---

func (a *matrixAdapter) baseURL() string {
	return a.homeserver
}

func (a *matrixAdapter) apiGet(path string) (map[string]any, error) {
	req, err := http.NewRequest("GET", a.baseURL()+"/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)

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

func (a *matrixAdapter) apiGetRaw(path string) (map[string]any, error) {
	req, err := http.NewRequest("GET", a.baseURL()+"/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)

	// Use a longer timeout for sync endpoints
	client := &http.Client{Timeout: (time.Duration(matrixSyncTimeout) + 10) * time.Second}
	resp, err := client.Do(req)
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

func (a *matrixAdapter) apiPut(path string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("PUT", a.baseURL()+"/"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.conn.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PUT %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("PUT %s → %d: %s", path, resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("PUT %s decode: %w", path, err)
	}
	return result, nil
}

func (a *matrixAdapter) apiPost(path string, payload map[string]any) (map[string]any, error) {
	var bodyReader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest("POST", a.baseURL()+"/"+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

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
		// Some endpoints return empty body
		return nil, nil
	}
	return result, nil
}

func (a *matrixAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformMatrix,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}
