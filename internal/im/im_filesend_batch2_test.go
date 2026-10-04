package im

// #3316 batch-2 probes: discord + slack arbitrary-file delivery over
// httptest; matrix is pinned by the compile-time interface assertion
// (its client stack needs a homeserver mock that outlives this batch's
// scope - the m.image sibling path already carries matrix coverage).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	_ FileSender = (*matrixAdapter)(nil)
	_ FileSender = (*discordAdapter)(nil)
	_ FileSender = (*slackAdapter)(nil)
	_ FileSender = (*tgAdapter)(nil)
	_ FileSender = (*qqAdapter)(nil)
)

func TestIssue3316_DiscordSendFileDocument(t *testing.T) {
	var gotPath string
	var gotPayload string
	var fileBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			gotPayload = r.FormValue("payload_json")
			if f, _, err := r.FormFile("files[0]"); err == nil {
				fileBytes, _ = io.ReadAll(f)
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	a := &discordAdapter{name: "dc", apiBase: srv.URL, httpClient: srv.Client(), connected: true}
	binding := ChannelBinding{Adapter: "dc", ChannelID: "chn-1"}
	file := OutboundFile{Path: "/tmp/a.tar.gz", Filename: "a.tar.gz", MIME: "application/gzip", Data: []byte("GZDATA")}
	if err := a.SendFile(context.Background(), binding, file, "artifacts"); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if gotPath != "/channels/chn-1/messages" {
		t.Fatalf("wrong endpoint: %s", gotPath)
	}
	if !strings.Contains(gotPayload, "artifacts") {
		t.Fatalf("caption must ride payload_json content: %q", gotPayload)
	}
	if !strings.Contains(string(fileBytes), "GZDATA") {
		t.Fatal("document bytes must be the payload")
	}
}

func TestIssue3316_SlackSendFileDocument(t *testing.T) {
	var gotPath string
	var gotChannels, gotComment string
	var fileBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			gotChannels = r.FormValue("channels")
			gotComment = r.FormValue("initial_comment")
			if f, _, err := r.FormFile("file"); err == nil {
				fileBytes, _ = io.ReadAll(f)
			}
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := &slackAdapter{name: "sl", apiBase: srv.URL, httpClient: srv.Client(), botToken: "xoxb", connected: true}
	binding := ChannelBinding{Adapter: "sl", ChannelID: "C123"}
	file := OutboundFile{Path: "/tmp/svc.log", Filename: "svc.log", MIME: "text/plain", Data: []byte("LOG")}
	if err := a.SendFile(context.Background(), binding, file, "latest log"); err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/files.upload") {
		t.Fatalf("wrong endpoint: %s", gotPath)
	}
	if gotChannels != "C123" || gotComment != "latest log" {
		t.Fatalf("channels/comment wrong: %q/%q", gotChannels, gotComment)
	}
	if !strings.Contains(string(fileBytes), "LOG") {
		t.Fatal("file bytes must be the payload")
	}
}
