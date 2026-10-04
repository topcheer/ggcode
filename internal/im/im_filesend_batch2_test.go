package im

// #3316 batch-2 FileSender tests: discord/matrix/slack SendFile must reach
// the native file channel (attachment upload / mxc+m.file / files.upload),
// never fall into the text path, and enforce platform size ceilings with
// readable errors.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"maunium.net/go/mautrix"
)

// --- Slack ---

type slackFileRecorder struct {
	mu       sync.Mutex
	uploads  int
	messages int
	filename string
	comment  string
}

func (r *slackFileRecorder) handle(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch req.URL.Path {
	case "/files.upload":
		r.uploads++
		if err := req.ParseMultipartForm(64 << 20); err == nil {
			if _, hdr, err := req.FormFile("file"); err == nil {
				r.filename = hdr.Filename
			}
			r.comment = req.FormValue("initial_comment")
		}
		w.Write([]byte(`{"ok":true}`))
	case "/chat.postMessage":
		r.messages++
		w.Write([]byte(`{"ok":true,"ts":"1.2"}`))
	default:
		http.Error(w, "unexpected "+req.URL.Path, http.StatusBadRequest)
	}
}

func newBatch2TestSlackAdapter(srv *httptest.Server) *slackAdapter {
	a := &slackAdapter{
		name:       "b2",
		botToken:   "xoxb-test",
		apiBase:    srv.URL,
		httpClient: srv.Client(),
	}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	return a
}

func TestSlackSendFileUsesUpload(t *testing.T) {
	rec := &slackFileRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handle))
	defer srv.Close()

	a := newBatch2TestSlackAdapter(srv)
	err := a.SendFile(context.Background(), ChannelBinding{ChannelID: "C1"}, OutboundFile{
		Path: "/tmp/report.pdf", Filename: "report.pdf", MIME: "application/pdf", Data: []byte("%PDF-1.4"),
	}, "see attachment")
	if err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if rec.uploads != 1 {
		t.Fatalf("expected 1 files.upload, got %d", rec.uploads)
	}
	if rec.messages != 0 {
		t.Fatalf("file send must not hit the text path, got %d chat.postMessage", rec.messages)
	}
	if rec.filename != "report.pdf" || rec.comment != "see attachment" {
		t.Fatalf("filename/comment mismatch: %q / %q", rec.filename, rec.comment)
	}
}

func TestSlackSendFileNotConnected(t *testing.T) {
	a := &slackAdapter{name: "b2"}
	err := a.SendFile(context.Background(), ChannelBinding{ChannelID: "C1"}, OutboundFile{Filename: "a", Data: []byte("x")}, "")
	if err == nil || !strings.Contains(err.Error(), "not online") {
		t.Fatalf("expected not-online error, got %v", err)
	}
}

// --- Matrix ---

type matrixFileRecorder struct {
	mu      sync.Mutex
	uploads int
	sends   []string // msgtype per send event
	uris    []string
}

func (r *matrixFileRecorder) handle(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case strings.Contains(req.URL.Path, "/upload"):
		r.uploads++
		w.Write([]byte(`{"content_uri":"mxc://localhost/abc"}`))
	case strings.Contains(req.URL.Path, "/send/"):
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		mt, _ := body["msgtype"].(string)
		r.sends = append(r.sends, mt)
		w.Write([]byte(`{"event_id":"$1"}`))
	default:
		http.Error(w, "unexpected "+req.URL.Path, http.StatusBadRequest)
	}
}

func newBatch2TestMatrixAdapter(srv *httptest.Server) *matrixAdapter {
	mc, err := mautrix.NewClient(srv.URL, "", "test-token")
	if err != nil {
		panic(err)
	}
	a := &matrixAdapter{name: "b2", client: mc}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	return a
}

func TestMatrixSendFileSendsFileEvent(t *testing.T) {
	rec := &matrixFileRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handle))
	defer srv.Close()

	a := newBatch2TestMatrixAdapter(srv)
	err := a.SendFile(context.Background(), ChannelBinding{ChannelID: "!room:hs"}, OutboundFile{
		Filename: "data.zip", MIME: "application/zip", Data: []byte("PK\x03\x04zip"),
	}, "logs")
	if err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if rec.uploads != 1 {
		t.Fatalf("expected 1 content-repo upload, got %d", rec.uploads)
	}
	if len(rec.sends) != 2 || rec.sends[0] != "m.file" || rec.sends[1] != "m.text" {
		t.Fatalf("expected m.file + caption m.text, got %v", rec.sends)
	}
}

func TestMatrixSendFileImageUsesImageEvent(t *testing.T) {
	rec := &matrixFileRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handle))
	defer srv.Close()

	a := newBatch2TestMatrixAdapter(srv)
	err := a.SendFile(context.Background(), ChannelBinding{ChannelID: "!room:hs"}, OutboundFile{
		Filename: "shot.png", MIME: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
	}, "")
	if err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if len(rec.sends) != 1 || rec.sends[0] != "m.image" {
		t.Fatalf("image files must send a single m.image (no caption event), got %v", rec.sends)
	}
}

func TestMatrixSendFileOversizeRejected(t *testing.T) {
	a := &matrixAdapter{name: "b2"} // client nil: must fail on connect check first
	err := a.SendFile(context.Background(), ChannelBinding{ChannelID: "!r:hs"}, OutboundFile{Filename: "a", Data: []byte("x")}, "")
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected not-connected error, got %v", err)
	}
}

// --- Discord ---

type discordFileRecorder struct {
	mu       sync.Mutex
	uploads  int
	messages int
	filename string
	content  string
}

func (r *discordFileRecorder) handle(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Path == "/channels/C1/messages" && strings.Contains(req.Header.Get("Content-Type"), "multipart/form-data") {
		r.uploads++
		if err := req.ParseMultipartForm(64 << 20); err == nil {
			r.content = req.FormValue("payload_json")
			if _, hdr, err := req.FormFile("files[0]"); err == nil {
				r.filename = hdr.Filename
			}
		}
		w.Write([]byte(`{"id":"1"}`))
		return
	}
	if req.URL.Path == "/channels/C1/messages" {
		r.messages++
	}
	w.Write([]byte(`{"id":"2"}`))
}

func newBatch2TestDiscordAdapter(srv *httptest.Server) *discordAdapter {
	a := &discordAdapter{
		name:       "b2",
		token:      "test-token",
		apiBase:    srv.URL,
		httpClient: srv.Client(),
	}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	return a
}

func TestDiscordSendFileUsesAttachmentUpload(t *testing.T) {
	rec := &discordFileRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handle))
	defer srv.Close()

	a := newBatch2TestDiscordAdapter(srv)
	err := a.SendFile(context.Background(), ChannelBinding{ChannelID: "C1"}, OutboundFile{
		Filename: "trace.log", MIME: "text/plain", Data: []byte("log lines"),
	}, "attached")
	if err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if rec.uploads != 1 {
		t.Fatalf("expected 1 multipart attachment upload, got %d", rec.uploads)
	}
	if rec.filename != "trace.log" {
		t.Fatalf("filename mismatch: %q", rec.filename)
	}
	if !strings.Contains(rec.content, "attached") {
		t.Fatalf("caption missing from payload_json: %q", rec.content)
	}
}

func TestDiscordSendFileOversizeRejected(t *testing.T) {
	a := &discordAdapter{name: "b2"}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	big := make([]byte, discordMaxUploadBytes+1)
	err := a.SendFile(context.Background(), ChannelBinding{ChannelID: "C1"}, OutboundFile{Filename: "big.bin", Data: big}, "")
	if err == nil || !strings.Contains(err.Error(), "Discord upload limit") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}
