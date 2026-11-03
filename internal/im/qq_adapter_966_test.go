package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func tinyPNGBase64(t *testing.T, r, g, b uint8) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: r, G: g, B: b, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

type qqSentRequest struct {
	Path    string
	Body    map[string]any
	RawBody string
}

func newQQSendTestAdapter(t *testing.T) (*qqAdapter, *[]qqSentRequest) {
	t.Helper()
	var sent []qqSentRequest
	uploadCount := 0
	adapter := &qqAdapter{
		name:           "hermes",
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := readAllForTest(req)
			var parsed map[string]any
			_ = json.Unmarshal(body, &parsed)
			sent = append(sent, qqSentRequest{Path: req.URL.Path, Body: parsed, RawBody: string(body)})
			respBody := `{}` //nolint:goconst // minimal success envelope
			if strings.HasSuffix(req.URL.Path, "/files") {
				uploadCount++
				respBody = `{"file_info":"fi-` + strconv.Itoa(uploadCount) + `"}`
			}
			return jsonResponse(respBody), nil
		})},
		chatTypes:   map[string]string{"group-1": "group"},
		seen:        map[string]time.Time{},
		uploadCache: map[string]qqUploadCacheEntry{},
	}
	return adapter, &sent
}

func readAllForTest(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(req.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func seqsOf(reqs []qqSentRequest) []int {
	var out []int
	for _, r := range reqs {
		if v, ok := r.Body["msg_seq"]; ok {
			if f, ok := v.(float64); ok {
				out = append(out, int(f))
			}
		}
	}
	return out
}

func isConsecutiveFrom(seqs []int, start int) bool {
	for i, s := range seqs {
		if s != start+i {
			return false
		}
	}
	return len(seqs) > 0
}

func TestQQSendMsgSeqUniqueImagesAndChunks(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
	img1 := tinyPNGBase64(t, 255, 0, 0)
	img2 := tinyPNGBase64(t, 0, 255, 0)
	limit := PlatformLimits[PlatformQQ]
	if limit <= 0 {
		t.Fatalf("PlatformLimits[PlatformQQ] not configured")
	}
	content := "![a](data:image/png;base64," + img1 + ") ![b](data:image/png;base64," + img2 + ") " + strings.Repeat("chunk ", limit)

	binding := ChannelBinding{
		Workspace:            "ws",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-42",
		PassiveReplyCount:    1,
	}
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	all := *sent

	var messages []qqSentRequest
	for _, r := range all {
		if _, ok := r.Body["msg_type"]; ok {
			messages = append(messages, r)
		}
	}
	if len(messages) < 4 {
		t.Fatalf("expected >= 4 message requests (2 images + >=2 chunks), got %d of %d total", len(messages), len(all))
	}
	media, text := 0, 0
	for _, r := range messages {
		switch mt := r.Body["msg_type"]; mt {
		case float64(qqMsgTypeMedia):
			media++
		case float64(qqMsgTypeText), float64(qqMsgTypeMarkdown):
			text++
		default:
			t.Fatalf("unexpected msg_type %v in body %s", mt, r.RawBody)
		}
	}
	if media != 2 {
		t.Fatalf("expected 2 media requests, got %d", media)
	}
	if text < 2 {
		t.Fatalf("expected >= 2 text chunk requests, got %d", text)
	}

	seqs := seqsOf(messages)
	seen := make(map[int]bool)
	for _, s := range seqs {
		if seen[s] {
			t.Fatalf("msg_seq collision detected: %d used twice (seqs=%v)", s, seqs)
		}
		seen[s] = true
	}
	if !isConsecutiveFrom(seqs, 2) {
		t.Fatalf("expected msg_seq values to be consecutive starting at 2 (replySeq), got %v", seqs)
	}

	for i, r := range messages {
		if r.Body["msg_type"] == float64(qqMsgTypeMedia) && r.Body["msg_id"] != "msg-42" {
			t.Fatalf("media request %d missing msg_id reply anchor: %s", i, r.RawBody)
		}
	}
}

func TestQQSendPureTextChunkSeqsContinueCursor(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
	limit := PlatformLimits[PlatformQQ]
	content := strings.Repeat("word ", (limit/5)*3)

	binding := ChannelBinding{
		Workspace:            "ws",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-42",
		PassiveReplyCount:    1,
	}
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	seqs := seqsOf(*sent)
	if len(seqs) < 3 {
		t.Fatalf("expected >= 3 chunks, got %d (seqs=%v)", len(seqs), seqs)
	}
	if !isConsecutiveFrom(seqs, 2) {
		t.Fatalf("chunk msg_seq must be consecutive from replySeq=2, got %v", seqs)
	}
}

func TestQQSendActiveModeOmitsSeq(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
	binding := ChannelBinding{Workspace: "ws", ChannelID: "group-1"}
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: "hello"}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*sent))
	}
	if _, ok := (*sent)[0].Body["msg_seq"]; ok {
		t.Fatalf("active send must not carry msg_seq: %s", (*sent)[0].RawBody)
	}
}

func newQQWSTestAdapter(t *testing.T) (*qqAdapter, chan string, func()) {
	t.Helper()
	frames := make(chan string, 8)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			select {
			case frames <- string(msg):
			default:
			}
		}
	}))
	t.Cleanup(server.Close)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dial := func() *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial test ws: %v", err)
		}
		return conn
	}
	conn := dial()
	t.Cleanup(func() { conn.Close() })
	adapter := &qqAdapter{
		name:           "hermes",
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             conn,
		seen:           map[string]time.Time{},
	}
	redial := func() {
		newConn := dial()
		t.Cleanup(func() { newConn.Close() })
		adapter.mu.Lock()
		adapter.ws = newConn
		adapter.mu.Unlock()
	}
	return adapter, frames, redial
}

func waitFrame(t *testing.T, frames chan string) string {
	t.Helper()
	select {
	case f := <-frames:
		return f
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for gateway frame")
		return ""
	}
}

func TestQQGatewayOp7RequestsReconnectKeepingSession(t *testing.T) {
	adapter, _, _ := newQQWSTestAdapter(t)
	adapter.sessionID = "sess-9"
	adapter.lastSeq = 42

	err := adapter.handleGatewayPayload(context.Background(), []byte(`{"op":7,"d":null}`))
	if err == nil || !strings.Contains(err.Error(), "reconnect") {
		t.Fatalf("op7 must surface errQQReconnect, got %v", err)
	}
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	if adapter.sessionID != "sess-9" || adapter.lastSeq != 42 {
		t.Fatalf("op7 must keep session for RESUME, got session=%q seq=%d", adapter.sessionID, adapter.lastSeq)
	}
}

func TestQQGatewayOp9InvalidSessionNotResumable(t *testing.T) {
	adapter, _, _ := newQQWSTestAdapter(t)
	adapter.sessionID = "sess-9"
	adapter.lastSeq = 42

	err := adapter.handleGatewayPayload(context.Background(), []byte(`{"op":9,"d":false}`))
	if err == nil || !strings.Contains(err.Error(), "reconnect") {
		t.Fatalf("op9 must surface errQQReconnect, got %v", err)
	}
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	if adapter.sessionID != "" || adapter.lastSeq != 0 {
		t.Fatalf("op9 d=false must clear session, got session=%q seq=%d", adapter.sessionID, adapter.lastSeq)
	}
}

func TestQQGatewayOp9ResumableKeepsSession(t *testing.T) {
	adapter, _, _ := newQQWSTestAdapter(t)
	adapter.sessionID = "sess-9"
	adapter.lastSeq = 42

	if err := adapter.handleGatewayPayload(context.Background(), []byte(`{"op":9,"d":true}`)); err == nil {
		t.Fatalf("op9 must still request reconnect")
	}
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	if adapter.sessionID != "sess-9" || adapter.lastSeq != 42 {
		t.Fatalf("op9 d=true must keep session for resume, got session=%q seq=%d", adapter.sessionID, adapter.lastSeq)
	}
}

func TestQQGatewayHelloResumesStoredSession(t *testing.T) {
	adapter, frames, _ := newQQWSTestAdapter(t)
	adapter.sessionID = "sess-9"
	adapter.lastSeq = 42

	if err := adapter.handleGatewayPayload(context.Background(), []byte(`{"op":10,"d":{"heartbeat_interval":30000}}`)); err != nil {
		t.Fatalf("handleGatewayPayload: %v", err)
	}
	frame := waitFrame(t, frames)
	var payload map[string]any
	if err := json.Unmarshal([]byte(frame), &payload); err != nil {
		t.Fatalf("parse frame %s: %v", frame, err)
	}
	if op, _ := payload["op"].(float64); op != 6 {
		t.Fatalf("expected op 6 RESUME, got frame %s", frame)
	}
	d, _ := payload["d"].(map[string]any)
	if d["session_id"] != "sess-9" || d["seq"] != float64(42) {
		t.Fatalf("resume payload must carry session_id+seq, got %s", frame)
	}
}

func TestQQGatewayHelloIdentifiesWithoutSession(t *testing.T) {
	adapter, frames, _ := newQQWSTestAdapter(t)
	if err := adapter.handleGatewayPayload(context.Background(), []byte(`{"op":10,"d":{"heartbeat_interval":30000}}`)); err != nil {
		t.Fatalf("handleGatewayPayload: %v", err)
	}
	frame := waitFrame(t, frames)
	var payload map[string]any
	if err := json.Unmarshal([]byte(frame), &payload); err != nil {
		t.Fatalf("parse frame %s: %v", frame, err)
	}
	if op, _ := payload["op"].(float64); op != 2 {
		t.Fatalf("expected op 2 IDENTIFY without stored session, got %s", frame)
	}
}

func TestQQGatewayOp9ThenHelloIdentifies(t *testing.T) {
	adapter, frames, redial := newQQWSTestAdapter(t)
	adapter.sessionID = "sess-9"
	adapter.lastSeq = 42

	if err := adapter.handleGatewayPayload(context.Background(), []byte(`{"op":9,"d":false}`)); err == nil {
		t.Fatalf("op9 must request reconnect")
	}

	redial()
	if err := adapter.handleGatewayPayload(context.Background(), []byte(`{"op":10,"d":{}}`)); err != nil {
		t.Fatalf("handleGatewayPayload: %v", err)
	}
	frame := waitFrame(t, frames)
	var payload map[string]any
	if err := json.Unmarshal([]byte(frame), &payload); err != nil {
		t.Fatalf("parse frame %s: %v", frame, err)
	}
	if op, _ := payload["op"].(float64); op != 2 {
		t.Fatalf("expected fresh IDENTIFY after non-resumable op9, got %s", frame)
	}
}

func newQQQuotaAdapter(t *testing.T) (*qqAdapter, *ChannelBinding) {
	t.Helper()
	adapter, _ := newQQSendTestAdapter(t)
	mgr := NewManager()
	stored := &ChannelBinding{
		Workspace:            "ws",
		Adapter:              "hermes",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-42",
		PassiveReplyCount:    1,
	}
	mgr.currentBindings["hermes"] = stored
	adapter.manager = mgr
	return adapter, stored
}

func TestQQSendQuotaCountsImagesAndChunks(t *testing.T) {
	adapter, stored := newQQQuotaAdapter(t)
	img1 := tinyPNGBase64(t, 255, 0, 0)
	img2 := tinyPNGBase64(t, 0, 255, 0)
	limit := PlatformLimits[PlatformQQ]
	content := "![a](data:image/png;base64," + img1 + ") ![b](data:image/png;base64," + img2 + ") " + strings.Repeat("chunk ", limit)

	binding := *stored
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if got := stored.PassiveReplyCount; got < 1+4 {
		t.Fatalf("PassiveReplyCount must advance by consumed seq count (>=4), got %d", got)
	}
}

func TestQQSendQuotaCountsPureImages(t *testing.T) {
	adapter, stored := newQQQuotaAdapter(t)
	img1 := tinyPNGBase64(t, 255, 0, 0)
	img2 := tinyPNGBase64(t, 0, 0, 255)

	content := "![](data:image/png;base64," + img1 + ")![](data:image/png;base64," + img2 + ")"

	binding := *stored
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if got := stored.PassiveReplyCount; got != 1+2 {
		t.Fatalf("pure image send must advance count by 2, got %d", got)
	}
}

func TestQQSendQuotaSkipsWhenNothingConsumed(t *testing.T) {
	adapter, stored := newQQQuotaAdapter(t)
	binding := ChannelBinding{Workspace: "ws", ChannelID: "group-1", LastInboundMessageID: "msg-42"}
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: "hi"}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if got := stored.PassiveReplyCount; got != 1+1 {
		t.Fatalf("single chunk must advance count by exactly 1, got %d", got)
	}
}

func TestQQMentionPrefixStripsAngleOpenID(t *testing.T) {
	cases := map[string]string{
		"<@!ABC123> hello":   "hello",
		"<@ABC123> hello":    "hello",
		"@botname hello":     "hello",
		"<@!ABC123>hello":    "hello",
		"no mention here":    "no mention here",
		"<@!ABC123>  spaced": "spaced",
	}
	for in, want := range cases {
		if got := strings.TrimSpace(qqMentionPrefix.ReplaceAllString(in, "")); got != want {
			t.Errorf("mention strip %q = %q, want %q", in, got, want)
		}
	}
}

func TestQQRefreshTokenSurfacesHTTPStatusFirst(t *testing.T) {
	adapter := &qqAdapter{
		name: "hermes",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("<html>bad gateway page</html>")),
			}, nil
		})},
	}
	_, err := adapter.refreshTokenOnce(context.Background())
	if err == nil {
		t.Fatalf("expected error for 502 token response")
	}
	if !strings.Contains(err.Error(), "QQ token request failed [502]") {
		t.Fatalf("error must surface HTTP status before decode, got: %v", err)
	}
	if strings.Contains(err.Error(), "parse QQ token response") {
		t.Fatalf("error must not be a decode error, got: %v", err)
	}
}

func TestQQStripMentionPrefixBothEventTypes(t *testing.T) {
	cases := []struct {
		event string
		in    string
		want  string
	}{
		{"GROUP_AT_MESSAGE_CREATE", "<@!openid123> help me", "help me"},
		{"GUILD_AT_MESSAGE_CREATE", "<@userid456> check this error", "check this error"},
		{"GUILD_AT_MESSAGE_CREATE", "<@!botid> 帮我看看", "帮我看看"},

		{"C2C_MESSAGE_CREATE", "<@userid456> raw", "<@userid456> raw"},
	}
	for _, c := range cases {
		if got := qqStripMentionPrefix(c.event, c.in); got != c.want {
			t.Errorf("qqStripMentionPrefix(%s, %q) = %q, want %q", c.event, c.in, got, c.want)
		}
	}
}

func TestQQSendImageRateLimitGap(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
	img1 := tinyPNGBase64(t, 255, 0, 0)
	img2 := tinyPNGBase64(t, 0, 255, 0)
	content := "![a](data:image/png;base64," + img1 + ") ![b](data:image/png;base64," + img2 + ") hello"
	binding := ChannelBinding{
		Workspace:            "ws",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-7",
		PassiveReplyCount:    1,
	}
	start := time.Now()
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	elapsed := time.Since(start)

	minElapsed := qqInterMessageDelay * 2 * 3 / 4
	if elapsed < minElapsed {
		t.Errorf("multi-image send completed in %v; expected >= %v of inter-message spacing (QQ 5 msg/s limit, #1230)", elapsed, minElapsed)
	}
	msgs := 0
	for _, r := range *sent {
		if _, ok := r.Body["msg_type"]; ok {
			msgs++
		}
	}
	if msgs != 3 {
		t.Fatalf("expected 3 message sends (2 images + 1 text), got %d", msgs)
	}
}
