package im

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeWeComServer accepts a WebSocket, answers upload init/chunk/finish and
// aibot_send_msg acks, and records every received frame.
type fakeWeComServer struct {
	srv      *httptest.Server
	url      string
	mu       sync.Mutex
	frames   []map[string]any
	mediaID  string // returned by finish
	initFail bool
	// chunkDrops: drop the ack for the first N upload-chunk frames (#1254
	// retry test) - the adapter must re-send the idempotent chunk.
	chunkDrops int
}

func newFakeWeComServer(t *testing.T) *fakeWeComServer {
	t.Helper()
	f := &fakeWeComServer{mediaID: "MEDIA123"}
	up := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			var frame map[string]any
			if err := ws.ReadJSON(&frame); err != nil {
				return
			}
			cmd, _ := frame["cmd"].(string)
			f.mu.Lock()
			f.frames = append(f.frames, frame)
			f.mu.Unlock()

			ack := map[string]any{
				"headers": frame["headers"],
				"errcode": 0,
				"errmsg":  "ok",
			}
			switch cmd {
			case "ping":
				// subscribe-style frames: ack with empty body
			case wecomCmdUploadInit:
				if f.initFail {
					ack["errcode"] = 44001
					ack["errmsg"] = "media size out of range"
				} else {
					ack["body"] = map[string]any{"upload_id": "UP1"}
				}
			case wecomCmdUploadChunk:
				f.mu.Lock()
				drop := f.chunkDrops > 0
				if drop {
					f.chunkDrops--
				}
				f.mu.Unlock()
				if drop {
					continue // no ack: simulates a lost chunk ack
				}
			case wecomCmdUploadFinish:
				ack["body"] = map[string]any{"type": "image", "media_id": f.mediaID, "created_at": "1700000000"}
			}
			if err := ws.WriteJSON(ack); err != nil {
				return
			}
		}
	}))
	f.url = "ws" + strings.TrimPrefix(f.srv.URL, "http")
	return f
}

func (f *fakeWeComServer) cmdCount(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, fr := range f.frames {
		if fr["cmd"] == cmd {
			n++
		}
	}
	return n
}

func newWecomMediaAdapter(t *testing.T, f *fakeWeComServer) *wecomAdapter {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(f.url, nil)
	if err != nil {
		t.Fatalf("dial fake wecom: %v", err)
	}
	t.Cleanup(func() { ws.Close() })

	a := &wecomAdapter{
		name:        "test",
		ws:          ws,
		connected:   true,
		seen:        map[string]time.Time{},
		replyReqIDs: map[string]string{},
		ackTimeout:  2 * time.Second,
	}
	// Drain server acks into dispatchPayload like the real read loop does.
	go func() {
		for {
			var payload map[string]any
			if err := ws.ReadJSON(&payload); err != nil {
				return
			}
			a.dispatchPayload(t.Context(), payload)
		}
	}()
	return a
}

// TestWecomUploadAndSendImage pins the full media flow: init → N chunks →
// finish → aibot_send_msg with msgtype=image and the returned media_id.
func TestWecomUploadAndSendImage(t *testing.T) {
	f := newFakeWeComServer(t)
	defer f.srv.Close()
	a := newWecomMediaAdapter(t, f)

	// 600KB → 2 chunks of 512KB + 88KB
	data := make([]byte, 600<<10)
	mediaID, err := a.wecomUploadMedia(t.Context(), data, "shot.png")
	if err != nil {
		t.Fatalf("wecomUploadMedia: %v", err)
	}
	if mediaID != "MEDIA123" {
		t.Fatalf("media_id = %q, want MEDIA123", mediaID)
	}

	// Send outside the f.mu critical section below: the fake server's
	// handler also takes f.mu when recording frames, so holding it here would
	// deadlock the ack path.
	if err := a.sendWecomImageMsg("chat-1", mediaID); err != nil {
		t.Fatalf("sendWecomImageMsg: %v", err)
	}

	// Chunk contents must round-trip: reconstruct from the recorded frames.
	f.mu.Lock()
	defer f.mu.Unlock()
	var chunks [][]byte
	var initBody map[string]any
	for _, fr := range f.frames {
		switch fr["cmd"] {
		case wecomCmdUploadInit:
			initBody, _ = fr["body"].(map[string]any)
		case wecomCmdUploadChunk:
			body, _ := fr["body"].(map[string]any)
			b64, _ := body["base64_data"].(string)
			raw, derr := base64.StdEncoding.DecodeString(b64)
			if derr != nil {
				t.Fatalf("chunk base64 decode: %v", derr)
			}
			chunks = append(chunks, raw)
		}
	}
	if initBody == nil {
		t.Fatal("no init frame recorded")
	}
	if got, _ := initBody["total_size"].(float64); int(got) != len(data) {
		t.Fatalf("init total_size = %v, want %d", initBody["total_size"], len(data))
	}
	if got, _ := initBody["total_chunks"].(float64); int(got) != 2 {
		t.Fatalf("init total_chunks = %v, want 2", initBody["total_chunks"])
	}
	if len(chunks) != 2 || len(chunks[0]) != wecomChunkBytes || len(chunks[1]) != len(data)-wecomChunkBytes {
		t.Fatalf("chunk sizes wrong: %d chunks", len(chunks))
	}

	found := false
	for _, fr := range f.frames {
		if fr["cmd"] == wecomCmdSend {
			body, _ := fr["body"].(map[string]any)
			if body["msgtype"] == "image" {
				img, _ := body["image"].(map[string]any)
				if img["media_id"] == "MEDIA123" && body["chatid"] == "chat-1" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("no aibot_send_msg image frame recorded")
	}
}

// TestWecomUploadRejectsOversized verifies the official 10MB image cap is
// enforced locally before any upload frames are sent.
func TestWecomUploadRejectsOversized(t *testing.T) {
	f := newFakeWeComServer(t)
	defer f.srv.Close()
	a := newWecomMediaAdapter(t, f)

	big := make([]byte, 10<<20+1)
	_, err := a.wecomUploadMedia(t.Context(), big, "big.png")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected oversize error, got %v", err)
	}
	if n := f.cmdCount(wecomCmdUploadInit); n != 0 {
		t.Fatalf("expected no init frame for oversized image, got %d", n)
	}
}

// TestWecomUploadInitRejected verifies server-side init errors surface.
func TestWecomUploadInitRejected(t *testing.T) {
	f := newFakeWeComServer(t)
	f.initFail = true
	defer f.srv.Close()
	a := newWecomMediaAdapter(t, f)

	_, err := a.wecomUploadMedia(t.Context(), []byte("pngdata-1234"), "x.png")
	if err == nil || !strings.Contains(err.Error(), "upload init") {
		t.Fatalf("expected init failure, got %v", err)
	}
}

// TestWecomResolveImageLocalPath verifies local files feed the upload.
func TestWecomResolveImageLocalPath(t *testing.T) {
	a := &wecomAdapter{}
	data, name, err := a.wecomResolveImageBytes(t.Context(), ExtractedImage{Kind: "url", Data: writeTempPNG(t)})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(data) == 0 || name != "img.png" {
		t.Fatalf("data=%d name=%q", len(data), name)
	}
}

// TestWechatImageURL pins which extracted kinds are deliverable via iLink.
func TestWechatImageURL(t *testing.T) {
	cases := []struct {
		img  ExtractedImage
		want string
	}{
		{ExtractedImage{Kind: "url", Data: "https://cdn.example.com/a.png"}, "https://cdn.example.com/a.png"},
		{ExtractedImage{Kind: "url", Data: "http://cdn.example.com/a.jpg"}, "http://cdn.example.com/a.jpg"},
		{ExtractedImage{Kind: "url", Data: "/tmp/local.png"}, ""},                  // local: undeliverable
		{ExtractedImage{Kind: "data_url", Data: "data:image/png;base64,AAA="}, ""}, // data URL: undeliverable
	}
	for i, tc := range cases {
		if got := wechatImageURL(tc.img); got != tc.want {
			t.Errorf("case %d: wechatImageURL = %q, want %q", i, got, tc.want)
		}
	}
}

// TestWechatSendSingleImage pins the iLink item structure: type 2 image_item
// carrying the URL.
func TestWechatSendSingleImage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAllForTest(r)
		json.Unmarshal(body, &gotBody)
		w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	a := &WechatAdapter{name: "test", baseURL: srv.URL, httpClient: srv.Client()}
	err := a.sendSingleImage(t.Context(), "tok", "user-1", "ctx-1", "https://x.example.com/i.png")
	if err != nil {
		t.Fatalf("sendSingleImage: %v", err)
	}

	msg, _ := gotBody["msg"].(map[string]any)
	items, _ := msg["item_list"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %v", msg["item_list"])
	}
	item, _ := items[0].(map[string]any)
	if item["type"] != float64(ilinkItemImage) {
		t.Fatalf("item type = %v, want %d", item["type"], ilinkItemImage)
	}
	img, _ := item["image_item"].(map[string]any)
	if img["image_url"] != "https://x.example.com/i.png" {
		t.Fatalf("image_url = %v", img["image_url"])
	}
}
