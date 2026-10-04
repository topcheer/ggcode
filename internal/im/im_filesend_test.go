package im

// #3316 probes: FileSender dispatch, QQ document path, Telegram document path.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeFileSink accepts arbitrary files and records them.
type fakeFileSink struct {
	name string
	err  error
	got  []OutboundFile
	caps []string
}

func (f *fakeFileSink) Name() string { return f.name }
func (f *fakeFileSink) Send(ctx context.Context, b ChannelBinding, e OutboundEvent) error {
	return nil
}
func (f *fakeFileSink) SendFile(ctx context.Context, b ChannelBinding, file OutboundFile, caption string) error {
	if f.err != nil {
		return f.err
	}
	f.got = append(f.got, file)
	f.caps = append(f.caps, caption)
	return nil
}

// plainSink does NOT implement FileSender.
type plainSink struct{ name string }

func (p *plainSink) Name() string { return p.name }
func (p *plainSink) Send(ctx context.Context, b ChannelBinding, e OutboundEvent) error {
	return nil
}

func TestIssue3316_ManagerDispatch(t *testing.T) {
	m := NewManager()
	fs := &fakeFileSink{name: "fs-adapter"}
	m.sinks["fs-adapter"] = fs
	m.sinks["plain-adapter"] = &plainSink{name: "plain-adapter"}

	binding := ChannelBinding{Adapter: "fs-adapter", ChannelID: "c1"}
	file := OutboundFile{Path: "/tmp/a.pdf", Filename: "a.pdf", MIME: "application/pdf", Data: []byte("pdf")}
	if err := m.SendFileDirect(context.Background(), binding, file, "cap"); err != nil {
		t.Fatalf("SendFileDirect failed: %v", err)
	}
	if len(fs.got) != 1 || fs.got[0].Filename != "a.pdf" || fs.caps[0] != "cap" {
		t.Fatalf("file sink must receive the file + caption, got %+v/%v", fs.got, fs.caps)
	}

	err := m.SendFileDirect(context.Background(), ChannelBinding{Adapter: "plain-adapter", ChannelID: "c1"}, file, "")
	if err != ErrFileUploadUnsupported {
		t.Fatalf("non-FileSender sink must return ErrFileUploadUnsupported, got %v", err)
	}
}

func TestIssue3316_QQSendFileDocumentAndCaption(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
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

	file := OutboundFile{Path: "/tmp/report.pdf", Filename: "report.pdf", MIME: "application/pdf", Data: []byte("%PDF-1.4 fake")}
	if err := adapter.SendFile(context.Background(), *stored, file, "见附件"); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}

	var uploadBody, mediaMsg, captionMsg map[string]any
	for _, r := range *sent {
		if strings.HasSuffix(r.Path, "/files") && uploadBody == nil {
			uploadBody = r.Body
		}
		if r.Body["msg_type"] != nil && mediaMsg == nil {
			mediaMsg = r.Body
		} else if r.Body["msg_type"] != nil && captionMsg == nil {
			captionMsg = r.Body
		}
	}
	if uploadBody == nil {
		t.Fatal("no file upload request captured")
	}
	if ft, _ := uploadBody["file_type"].(float64); int(ft) != qqFileTypeFile {
		t.Fatalf("generic file must upload with file_type=%d, got %v (body=%v)", qqFileTypeFile, uploadBody["file_type"], uploadBody)
	}
	if mediaMsg == nil || captionMsg == nil {
		t.Fatalf("expected media message + caption text message, got %d sends", len(*sent))
	}
	if _, ok := mediaMsg["media"]; !ok {
		t.Fatalf("media message must carry file_info media: %v", mediaMsg)
	}
	if s, _ := captionMsg["msg_seq"].(float64); int(s) != 3 { // count=1 -> media seq 2, caption seq 3
		t.Fatalf("caption must ride the next seq slot (3), got %v (msg=%v)", captionMsg["msg_seq"], captionMsg)
	}
	if stored.PassiveReplyCount != 3 {
		t.Fatalf("media + caption must each record a slot: count=%d, want 3", stored.PassiveReplyCount)
	}
}

func TestIssue3316_TGSendDocument(t *testing.T) {
	var gotPath string
	var gotForm map[string][]string
	var docBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			gotForm = r.Form
			if f, _, err := r.FormFile("document"); err == nil {
				docBytes, _ = io.ReadAll(f)
			}
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := &tgAdapter{name: "tg-test", httpClient: srv.Client(), botToken: "TOK", apiBase: srv.URL, connected: true, seen: map[int]time.Time{}}
	binding := ChannelBinding{Adapter: "tg-test", ChannelID: "12345"}
	file := OutboundFile{Path: "/tmp/app.log", Filename: "app.log", MIME: "text/plain", Data: []byte("log-line-1\n")}
	if err := a.SendFile(context.Background(), binding, file, "logs"); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if gotPath != "/botTOK/sendDocument" {
		t.Fatalf("document must go to sendDocument, got %s", gotPath)
	}
	if gotForm["chat_id"][0] != "12345" || gotForm["caption"][0] != "logs" {
		t.Fatalf("form fields wrong: %v", gotForm)
	}
	if !bytes.Contains(docBytes, []byte("log-line-1")) {
		t.Fatal("document bytes must be the file payload")
	}
}
