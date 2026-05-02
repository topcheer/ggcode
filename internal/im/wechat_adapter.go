package im

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	ilinkBaseURL           = "https://ilinkai.weixin.qq.com"
	ilinkGetQRCodePath     = "/ilink/bot/get_bot_qrcode"
	ilinkGetQRCodeStatus   = "/ilink/bot/get_qrcode_status"
	ilinkGetUpdatesPath    = "/ilink/bot/getupdates"
	ilinkSendMessagePath   = "/ilink/bot/sendmessage"
	ilinkLongPollTimeoutMs = 35000
)

// iLink message item types
const (
	ilinkItemText  = 1
	ilinkItemImage = 2
	ilinkItemVoice = 3
	ilinkItemFile  = 4
	ilinkItemVideo = 5
)

// iLink message types
const (
	ilinkMsgTypeUser = 1
	ilinkMsgTypeBot  = 2
)

// iLink message states
const (
	ilinkMsgStateNew        = 1
	ilinkMsgStateProcessing = 2
	ilinkMsgStateFinish     = 3
)

// WechatAdapter implements the Sink interface for WeChat iLink Bot API.
type WechatAdapter struct {
	name       string
	manager    *Manager
	httpClient *http.Client

	// Config
	baseURL string

	// Runtime state
	mu            sync.RWMutex
	connected     bool
	botToken      string
	cursor        string            // get_updates_buf cursor
	contextTokens map[string]string // msg_id → context_token for reply correlation
}

// ilinkQRCodeResponse is the response from get_bot_qrcode.
type ilinkQRCodeResponse struct {
	QRCode           string `json:"qrcode"`
	QRCodeImgContent string `json:"qrcode_img_content"`
}

// ilinkQRCodeStatusResponse is the response from get_qrcode_status.
type ilinkQRCodeStatusResponse struct {
	Status   string `json:"status"`
	BotToken string `json:"bot_token"`
	BaseURL  string `json:"baseurl"`
}

// ilinkGetUpdatesResponse is the response from getupdates.
type ilinkGetUpdatesResponse struct {
	Ret               int            `json:"ret"`
	ErrCode           int            `json:"errcode"`
	ErrMsg            string         `json:"errmsg"`
	Msgs              []ilinkMessage `json:"msgs"`
	GetUpdatesBuf     string         `json:"get_updates_buf"`
	LongPollTimeoutMs int            `json:"longpolling_timeout_ms"`
}

// ilinkMessage represents a single iLink message.
type ilinkMessage struct {
	Seq          int64       `json:"seq"`
	MessageID    int64       `json:"message_id"`
	FromUserID   string      `json:"from_user_id"`
	ToUserID     string      `json:"to_user_id"`
	ClientID     string      `json:"client_id"`
	CreateTimeMs int64       `json:"create_time_ms"`
	SessionID    string      `json:"session_id"`
	GroupID      string      `json:"group_id"`
	MessageType  int         `json:"message_type"`
	MessageState int         `json:"message_state"`
	ContextToken string      `json:"context_token"`
	ItemList     []ilinkItem `json:"item_list"`
}

// ilinkItem is a single item in an iLink message.
type ilinkItem struct {
	Type      int             `json:"type"`
	TextItem  *ilinkTextItem  `json:"text_item,omitempty"`
	ImageItem *ilinkImageItem `json:"image_item,omitempty"`
	FileItem  *ilinkFileItem  `json:"file_item,omitempty"`
}

type ilinkTextItem struct {
	Text string `json:"text"`
}

type ilinkImageItem struct {
	ImageURL string `json:"image_url"`
	AESKey   string `json:"aes_key"`
}

type ilinkFileItem struct {
	FileURL  string `json:"file_url"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

// ilinkSendMessageRequest is the request body for sendmessage.
type ilinkSendMessageRequest struct {
	Msg      ilinkOutboundMessage `json:"msg"`
	BaseInfo ilinkBaseInfo        `json:"base_info"`
}

type ilinkBaseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

type ilinkOutboundMessage struct {
	ToUserID     string      `json:"to_user_id"`
	ClientID     string      `json:"client_id"`
	MessageType  int         `json:"message_type"`
	MessageState int         `json:"message_state"`
	ContextToken string      `json:"context_token"`
	ItemList     []ilinkItem `json:"item_list"`
}

func newWechatAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*WechatAdapter, error) {
	baseURL := strings.TrimSpace(stringValue(adapterCfg.Extra, "base_url"))
	if baseURL == "" {
		baseURL = ilinkBaseURL
	}
	botToken := strings.TrimSpace(stringValue(adapterCfg.Extra, "bot_token"))
	return &WechatAdapter{
		name:          name,
		manager:       mgr,
		httpClient:    &http.Client{Timeout: 40 * time.Second},
		baseURL:       baseURL,
		botToken:      botToken,
		contextTokens: make(map[string]string),
	}, nil
}

func (a *WechatAdapter) Name() string { return a.name }

func (a *WechatAdapter) Start(ctx context.Context) {
	debug.Log("wechat", "adapter=%s start", a.name)
	if a.botToken == "" {
		a.publishState(false, "waiting_for_auth", "bot_token not configured — use /wechat to scan QR code")
		return
	}
	a.publishState(false, "connecting", "")
	safego.Go("im.wechat.run", func() { a.run(ctx) })
}

func (a *WechatAdapter) Close() error {
	a.mu.Lock()
	a.connected = false
	a.mu.Unlock()
	return nil
}

// AuthenticateQRCode requests a QR code from iLink for the user to scan.
// Returns the qrcode token (for status polling) and base64-encoded PNG image.
func (a *WechatAdapter) AuthenticateQRCode(ctx context.Context) (qrcode string, imgBase64 string, err error) {
	url := a.baseURL + ilinkGetQRCodePath + "?bot_type=3"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("create qrcode request: %w", err)
	}
	a.setCommonHeaders(req, "")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("qrcode request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("qrcode request failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var result ilinkQRCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode qrcode response: %w", err)
	}
	if result.QRCode == "" {
		return "", "", fmt.Errorf("empty qrcode token in response")
	}
	return result.QRCode, result.QRCodeImgContent, nil
}

// PollQRCodeStatus checks the QR code scan status. Returns status string and
// the bot_token when confirmed.
func (a *WechatAdapter) PollQRCodeStatus(ctx context.Context, qrcode string) (status string, botToken string, err error) {
	url := a.baseURL + ilinkGetQRCodeStatus + "?qrcode=" + qrcode
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("create status request: %w", err)
	}
	a.setCommonHeaders(req, "")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("status request: %w", err)
	}
	defer resp.Body.Close()
	var result ilinkQRCodeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode status response: %w", err)
	}
	return result.Status, result.BotToken, nil
}

// SetBotToken updates the bot token (after QR scan) and starts the message loop.
func (a *WechatAdapter) SetBotToken(token string) {
	a.mu.Lock()
	a.botToken = token
	a.mu.Unlock()
}

func (a *WechatAdapter) run(ctx context.Context) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	attempt := 0
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.pollLoop(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("wechat", "adapter=%s error: %v", a.name, err)
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

func (a *WechatAdapter) pollLoop(ctx context.Context) error {
	a.mu.Lock()
	token := a.botToken
	cursor := a.cursor
	a.mu.Unlock()

	if token == "" {
		return fmt.Errorf("no bot_token configured")
	}

	// Long-poll for messages
	body := map[string]interface{}{
		"get_updates_buf": cursor,
		"base_info": map[string]string{
			"channel_version": "1.0.0",
		},
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal getupdates: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+ilinkGetUpdatesPath, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("create getupdates request: %w", err)
	}
	a.setCommonHeaders(req, token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("getupdates request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("unauthorized — bot_token may be expired, re-scan required")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("getupdates failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var result ilinkGetUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode getupdates: %w", err)
	}

	// Check for session expiry or errors
	if result.ErrCode == -14 {
		return fmt.Errorf("session expired — bot_token may need re-scan")
	}
	if result.Ret != 0 {
		return fmt.Errorf("getupdates error: ret=%d errcode=%d errmsg=%s", result.Ret, result.ErrCode, result.ErrMsg)
	}

	// Update cursor
	if result.GetUpdatesBuf != "" {
		a.mu.Lock()
		a.cursor = result.GetUpdatesBuf
		a.mu.Unlock()
	}

	// Mark connected
	a.mu.Lock()
	if !a.connected {
		a.connected = true
	}
	a.mu.Unlock()
	a.publishState(true, "connected", "")

	// Process messages
	for _, msg := range result.Msgs {
		if msg.MessageType != ilinkMsgTypeUser {
			continue
		}
		a.handleMessage(ctx, msg)
	}
	return nil
}

func (a *WechatAdapter) handleMessage(ctx context.Context, msg ilinkMessage) {
	// Extract text from items
	var textParts []string
	for _, item := range msg.ItemList {
		if item.Type == ilinkItemText && item.TextItem != nil {
			textParts = append(textParts, item.TextItem.Text)
		}
	}
	text := strings.Join(textParts, "\n")
	if strings.TrimSpace(text) == "" {
		return
	}

	// Determine channel ID (group or direct)
	channelID := msg.FromUserID
	if msg.GroupID != "" {
		channelID = msg.GroupID
	}

	// Store context_token for reply correlation
	a.mu.Lock()
	a.contextTokens[channelID] = msg.ContextToken
	a.mu.Unlock()

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformWechat,
			ChannelID:  channelID,
			SenderID:   msg.FromUserID,
			MessageID:  strconv.FormatInt(msg.MessageID, 10),
			ReceivedAt: time.Now(),
		},
		Text: text,
		Metadata: map[string]string{
			"group_id": msg.GroupID,
		},
	}

	// Pairing flow: same as QQ adapter
	// 1. Try HandlePairingInbound — if consumed, reply with pairing instructions
	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		_ = a.sendTextToUser(ctx, channelID, pairingResult.ReplyText)
		if pairingResult.Bound && pairingResult.PreviousBinding != nil {
			if err := a.manager.SendDirect(ctx, *pairingResult.PreviousBinding, OutboundEvent{
				Kind: OutboundEventText,
				Text: "当前目录已绑定到其他渠道，如需重新绑定请再次发起配对。",
			}); err != nil {
				debug.Log("wechat", "adapter=%s notify previous channel: %v", a.name, err)
			}
		}
		return
	}

	// 2. Normal inbound routing
	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("wechat", "adapter=%s unauthorized inbound channel=%s", a.name, channelID)
			_ = a.sendTextToUser(ctx, channelID, "你是未授权用户")
			return
		}
		if err != ErrNoChannelBound {
			debug.Log("wechat", "adapter=%s handle inbound error: %v", a.name, err)
		}
	}
}

// sendTextToUser sends a plain text reply to a WeChat user/group.
func (a *WechatAdapter) sendTextToUser(ctx context.Context, toUserID, content string) error {
	a.mu.RLock()
	token := a.botToken
	contextToken := a.contextTokens[toUserID]
	a.mu.RUnlock()

	if token == "" || strings.TrimSpace(content) == "" {
		return nil
	}

	items := []ilinkItem{
		{Type: ilinkItemText, TextItem: &ilinkTextItem{Text: content}},
	}

	outMsg := ilinkSendMessageRequest{
		Msg: ilinkOutboundMessage{
			ToUserID:     toUserID,
			ClientID:     generateWechatClientID(),
			MessageType:  ilinkMsgTypeBot,
			MessageState: ilinkMsgStateFinish,
			ContextToken: contextToken,
			ItemList:     items,
		},
		BaseInfo: ilinkBaseInfo{ChannelVersion: "1.0.0"},
	}

	bodyJSON, err := json.Marshal(outMsg)
	if err != nil {
		return fmt.Errorf("marshal sendmessage: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+ilinkSendMessagePath, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("create sendmessage request: %w", err)
	}
	a.setCommonHeaders(req, token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sendmessage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sendmessage status=%d", resp.StatusCode)
	}
	return nil
}

// Send sends an outbound message to the bound WeChat channel.
func (a *WechatAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.mu.RLock()
	token := a.botToken
	contextToken := a.contextTokens[binding.ChannelID]
	a.mu.RUnlock()

	if token == "" {
		return fmt.Errorf("wechat adapter %q: no bot_token", a.name)
	}

	text := event.Text
	if text == "" {
		return nil
	}

	items := []ilinkItem{
		{Type: ilinkItemText, TextItem: &ilinkTextItem{Text: text}},
	}

	toUserID := binding.TargetID
	if toUserID == "" {
		toUserID = binding.ChannelID
	}

	outMsg := ilinkSendMessageRequest{
		Msg: ilinkOutboundMessage{
			ToUserID:     toUserID,
			ClientID:     generateWechatClientID(),
			MessageType:  ilinkMsgTypeBot,
			MessageState: ilinkMsgStateFinish,
			ContextToken: contextToken,
			ItemList:     items,
		},
		BaseInfo: ilinkBaseInfo{ChannelVersion: "1.0.0"},
	}

	bodyJSON, err := json.Marshal(outMsg)
	if err != nil {
		return fmt.Errorf("marshal sendmessage: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+ilinkSendMessagePath, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("create sendmessage request: %w", err)
	}
	a.setCommonHeaders(req, token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sendmessage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendmessage failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	return nil
}

// setCommonHeaders sets the required iLink headers.
func (a *WechatAdapter) setCommonHeaders(req *http.Request, token string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	// X-WECHAT-UIN: random uint32 → base64
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	uin := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
	req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(uin), 10))))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (a *WechatAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
	})
}

// GetAdapterState returns the current adapter state for the TUI panel.
func (a *WechatAdapter) GetState() AdapterState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	connected := a.connected
	token := a.botToken
	status := "disconnected"
	if token == "" {
		status = "waiting_for_auth"
	} else if connected {
		status = "connected"
	}
	return AdapterState{
		Name:    a.name,
		Healthy: connected,
		Status:  status,
	}
}

// generateWechatClientID generates a unique client ID for message sending.
// Matches SDK pattern: random hex suffix.
func generateWechatClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("go-weixin-%x", b)
}

// WechatAdapter returns the first wechat adapter sink from the manager, or nil.
func (m *Manager) WechatAdapter() *WechatAdapter {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sink := range m.sinks {
		if wa, ok := sink.(*WechatAdapter); ok {
			return wa
		}
	}
	return nil
}
