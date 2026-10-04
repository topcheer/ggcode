package im

// #3325 batch-3 probes: signal + mattermost arbitrary-file delivery over
// httptest; feishu/wecom/whatsapp are pinned by the compile-time interface
// assertions plus guard/limit probes (feishu's resolveAPIBase has no test
// injection point - same precedent as matrix in batch 2; wecom's chunked
// upload needs a live websocket; whatsapp's upload needs the whatsmeow
// media stack).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	_ FileSender = (*feishuAdapter)(nil)
	_ FileSender = (*wecomAdapter)(nil)
	_ FileSender = (*signalAdapter)(nil)
	_ FileSender = (*mattermostAdapter)(nil)
	_ FileSender = (*whatsappAdapter)(nil)
)

func TestIssue3325_SignalSendFileAttachment(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v2/send") {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("bad payload: %v", err)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	a := &signalAdapter{name: "sig", account: "+10000000000", baseURL: srv.URL, conn: srv.Client()}
	binding := ChannelBinding{Adapter: "sig", ChannelID: "+15550001111"}
	file := OutboundFile{Path: "/tmp/report.pdf", Filename: "report.pdf", MIME: "application/pdf", Data: []byte("%PDF-1.4")}
	if err := a.SendFile(context.Background(), binding, file, "weekly report"); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}

	if gotBody["message"] != "weekly report" {
		t.Fatalf("caption must ride the message field natively: %v", gotBody["message"])
	}
	recipients, _ := gotBody["recipients"].([]any)
	if len(recipients) != 1 || recipients[0] != "+15550001111" {
		t.Fatalf("recipients wrong: %v", gotBody["recipients"])
	}
	atts, ok := gotBody["base64_attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("base64_attachments missing: %v", gotBody["base64_attachments"])
	}
	att, _ := atts[0].(string)
	wantPrefix := "data:application/pdf;filename=report.pdf;base64,"
	if !strings.HasPrefix(att, wantPrefix) {
		t.Fatalf("attachment data-URL wrong: %q", att)
	}
	b64 := strings.TrimPrefix(att, wantPrefix)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || string(decoded) != "%PDF-1.4" {
		t.Fatalf("attachment bytes wrong: %q err=%v", b64, err)
	}
}

func TestIssue3325_SignalSendFileGroupRecipient(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	a := &signalAdapter{name: "sig", account: "+10000000000", baseURL: srv.URL, conn: srv.Client()}
	binding := ChannelBinding{Adapter: "sig", ChannelID: "group:abc"}
	if err := a.SendFile(context.Background(), binding, OutboundFile{Filename: "f.txt", MIME: "text/plain", Data: []byte("X")}, ""); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	recipients, _ := gotBody["recipients"].([]any)
	if len(recipients) != 1 {
		t.Fatalf("recipients missing: %v", gotBody["recipients"])
	}
	// applySignalRecipient double-base64-encodes group IDs ("group." prefix)
	r, _ := recipients[0].(string)
	if !strings.HasPrefix(r, "group.") {
		t.Fatalf("group recipient not double-encoded: %q", r)
	}
}

func TestIssue3325_MattermostSendFileDocument(t *testing.T) {
	var uploadPath, uploadQuery, uploadFilename, postBody string
	var fileBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/files"):
			uploadPath = r.URL.Path
			uploadQuery = r.URL.RawQuery
			if err := r.ParseMultipartForm(10 << 20); err == nil {
				if f, hdr, err := r.FormFile("files"); err == nil {
					uploadFilename = hdr.Filename
					fileBytes, _ = io.ReadAll(f)
				}
			}
			_, _ = w.Write([]byte(`[{"id":"FILEID1"}]`))
		case strings.Contains(r.URL.Path, "/posts"):
			body, _ := io.ReadAll(r.Body)
			postBody = string(body)
			_, _ = w.Write([]byte(`{"id":"POST1"}`))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := &mattermostAdapter{name: "mm", baseURL: srv.URL, token: "TOK", conn: srv.Client()}
	binding := ChannelBinding{Adapter: "mm", ChannelID: "chan-9"}
	file := OutboundFile{Path: "/tmp/build.log", Filename: "build.log", MIME: "text/plain", Data: []byte("BUILD OUTPUT")}
	if err := a.SendFile(context.Background(), binding, file, "latest build"); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}

	if !strings.Contains(uploadQuery, "channel_id=chan-9") {
		t.Fatalf("upload must target the channel: %s?%s", uploadPath, uploadQuery)
	}
	if uploadFilename != "build.log" {
		t.Fatalf("upload must keep the real filename, got %q", uploadFilename)
	}
	if !strings.Contains(string(fileBytes), "BUILD OUTPUT") {
		t.Fatal("document bytes must be the payload")
	}
	if !strings.Contains(postBody, "FILEID1") {
		t.Fatalf("post must attach file_ids: %s", postBody)
	}
	if !strings.Contains(postBody, "latest build") {
		t.Fatalf("caption must ride the post message natively: %s", postBody)
	}
}

func TestIssue3325_MattermostExtensionlessFilename(t *testing.T) {
	var uploadFilename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/files") {
			if _, hdr, err := r.FormFile("files"); err == nil {
				uploadFilename = hdr.Filename
			}
			_, _ = w.Write([]byte(`[{"id":"F"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"P"}`))
	}))
	defer srv.Close()

	a := &mattermostAdapter{name: "mm", baseURL: srv.URL, token: "TOK", conn: srv.Client()}
	binding := ChannelBinding{Adapter: "mm", ChannelID: "c"}
	// Extensionless name: the extension Mattermost keys previews on must be
	// derived from the MIME type.
	file := OutboundFile{Filename: "screenshot", MIME: "image/png", Data: []byte("PNG")}
	if err := a.SendFile(context.Background(), binding, file, ""); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if uploadFilename != "screenshot.png" {
		t.Fatalf("extensionless name must gain a MIME-derived ext, got %q", uploadFilename)
	}
}

func TestIssue3325_SendFileGuards(t *testing.T) {
	ctx := context.Background()
	file := OutboundFile{Filename: "f.bin", MIME: "application/octet-stream", Data: []byte("DATA")}

	// Empty binding → ErrNoChannelBound on all five adapters.
	cases := []struct {
		name string
		call func() error
	}{
		{"feishu", func() error {
			return (&feishuAdapter{name: "fs"}).SendFile(ctx, ChannelBinding{Adapter: "fs"}, file, "")
		}},
		{"wecom", func() error {
			return (&wecomAdapter{name: "wc"}).SendFile(ctx, ChannelBinding{Adapter: "wc"}, file, "")
		}},
		{"signal", func() error {
			return (&signalAdapter{name: "sg"}).SendFile(ctx, ChannelBinding{Adapter: "sg"}, file, "")
		}},
		{"mattermost", func() error {
			return (&mattermostAdapter{name: "mm"}).SendFile(ctx, ChannelBinding{Adapter: "mm"}, file, "")
		}},
	}
	for _, tc := range cases {
		if err := tc.call(); err != ErrNoChannelBound {
			t.Errorf("%s: empty binding must return ErrNoChannelBound, got %v", tc.name, err)
		}
	}
	// whatsapp checks connection BEFORE the binding (same order as its
	// Send), so an empty binding on a disconnected adapter reports the
	// connection error - an explicit failure either way.
	if err := (&whatsappAdapter{name: "wa"}).SendFile(ctx, ChannelBinding{Adapter: "wa"}, file, ""); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("whatsapp: empty binding on disconnected adapter must fail explicitly, got %v", err)
	}

	// Bound channel but not connected → explicit connection error, not a
	// silent nil deref or false success.
	bound := ChannelBinding{Adapter: "fs", ChannelID: "oc_chat"}
	if err := (&feishuAdapter{name: "fs"}).SendFile(ctx, bound, file, ""); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("feishu disconnected must fail explicitly, got %v", err)
	}
	bound.Adapter = "wc"
	if err := (&wecomAdapter{name: "wc"}).SendFile(ctx, bound, file, ""); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("wecom disconnected must fail explicitly, got %v", err)
	}
	bound.Adapter = "wa"
	if err := (&whatsappAdapter{name: "wa"}).SendFile(ctx, bound, file, ""); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("whatsapp disconnected must fail explicitly, got %v", err)
	}
}

func TestIssue3325_WecomFileCapDistinctFromImageCap(t *testing.T) {
	a := &wecomAdapter{name: "wc"}
	// Between the image cap (10MB) and the file cap (20MB): must pass the
	// file path's size gate (the probe below asserts the gate reads the
	// file cap, not the image cap) - a >20MB payload must fail before any
	// websocket traffic.
	big := make([]byte, wecomMaxFileBytes+1)
	_, err := a.wecomUploadMediaTyped(context.Background(), big, "big.bin", "file", wecomMaxFileBytes)
	if err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("oversized file must fail the size gate, got %v", err)
	}
	if strings.Contains(err.Error(), "image") {
		t.Fatalf("file path must not report the image wording: %v", err)
	}
	// Image path keeps its own cap and wording.
	_, err = a.wecomUploadMediaTyped(context.Background(), make([]byte, wecomMaxImageBytes+1), "big.png", "image", wecomMaxImageBytes)
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("oversized image must keep the image wording, got %v", err)
	}
}
