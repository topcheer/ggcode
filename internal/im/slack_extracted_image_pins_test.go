package im

// Pin tests for slackAdapter.sendExtractedImage (r207 seam extraction).
//
// Golden-filter discipline: these pins were written against the monolithic
// implementation and must run green BEFORE the behavior-preserving split.
// They lock exact error strings, fallback routing (URL-as-text via
// chat.postMessage), upload routing (/files.upload), filenames derived from
// local paths / remote URLs / data-URL mime, decoded payload bytes, and the
// channels/thread_ts form fields so the seam extraction cannot drift.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type slackPinFile struct {
	filename string
	data     []byte
}

type slackPinCapture struct {
	mu       sync.Mutex
	paths    []string
	channels []string
	threadTS []string
	comments []string
	texts    []string
	files    []slackPinFile
}

func (c *slackPinCapture) snapshot() (paths, channels, threadTS, comments, texts []string, files []slackPinFile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.paths...),
		append([]string{}, c.channels...),
		append([]string{}, c.threadTS...),
		append([]string{}, c.comments...),
		append([]string{}, c.texts...),
		append([]slackPinFile{}, c.files...)
}

func newSlackPinServer(t *testing.T) (*httptest.Server, *slackPinCapture) {
	t.Helper()
	capture := &slackPinCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.mu.Lock()
		capture.paths = append(capture.paths, r.URL.Path)
		capture.mu.Unlock()
		switch r.URL.Path {
		case "/files.upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			capture.mu.Lock()
			capture.channels = append(capture.channels, r.FormValue("channels"))
			capture.threadTS = append(capture.threadTS, r.FormValue("thread_ts"))
			capture.comments = append(capture.comments, r.FormValue("initial_comment"))
			for _, headers := range r.MultipartForm.File {
				for _, h := range headers {
					f, err := h.Open()
					if err != nil {
						t.Errorf("open part %q: %v", h.Filename, err)
						continue
					}
					data, err := io.ReadAll(f)
					f.Close()
					if err != nil {
						t.Errorf("read part %q: %v", h.Filename, err)
						continue
					}
					capture.files = append(capture.files, slackPinFile{filename: h.Filename, data: data})
				}
			}
			capture.mu.Unlock()
			w.Write([]byte(`{"ok":true}`))
		case "/chat.postMessage":
			var body struct {
				Channel string `json:"channel"`
				Text    string `json:"text"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			capture.mu.Lock()
			capture.channels = append(capture.channels, body.Channel)
			capture.texts = append(capture.texts, body.Text)
			capture.mu.Unlock()
			w.Write([]byte(`{"ok":true,"ts":"1700000000.000100"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func newSlackPinAdapter(srvURL string) *slackAdapter {
	return &slackAdapter{name: "pin", botToken: "xoxb-pin", apiBase: srvURL, httpClient: &http.Client{}}
}

func TestPinSlackSendExtractedImage_UnknownKindMessage(t *testing.T) {
	a := &slackAdapter{}
	err := a.sendExtractedImage(nil, "C1", "", ExtractedImage{Kind: "weird", Data: "x"})
	if err == nil || err.Error() != "unknown image kind: weird" {
		t.Fatalf("err = %v, want exact %q", err, "unknown image kind: weird")
	}
}

func TestPinSlackSendExtractedImage_DataURLUpload(t *testing.T) {
	srv, capture := newSlackPinServer(t)
	a := newSlackPinAdapter(srv.URL)
	payload := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	if err := a.sendExtractedImage(context.Background(), "C123", "T9", ExtractedImage{Kind: "data_url", Data: "data:image/png;base64," + payload}); err != nil {
		t.Fatalf("png data_url: %v", err)
	}
	if err := a.sendExtractedImage(context.Background(), "C123", "", ExtractedImage{Kind: "data_url", Data: "data:image/jpeg;base64," + payload}); err != nil {
		t.Fatalf("jpeg data_url: %v", err)
	}
	paths, channels, threadTS, comments, _, files := capture.snapshot()
	if len(paths) != 2 || paths[0] != "/files.upload" || paths[1] != "/files.upload" {
		t.Fatalf("paths = %v, want two /files.upload", paths)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	if files[0].filename != "image.png" || string(files[0].data) != "png-bytes" {
		t.Fatalf("png pin: filename=%q data=%q", files[0].filename, files[0].data)
	}
	if files[1].filename != "image.jpg" || string(files[1].data) != "png-bytes" {
		t.Fatalf("jpeg pin: filename=%q data=%q", files[1].filename, files[1].data)
	}
	if channels[0] != "C123" || channels[1] != "C123" {
		t.Fatalf("channels = %v", channels)
	}
	if threadTS[0] != "T9" {
		t.Fatalf("thread_ts[0] = %q, want T9", threadTS[0])
	}
	if threadTS[1] != "" {
		t.Fatalf("thread_ts[1] = %q, want empty", threadTS[1])
	}
	for i, c := range comments {
		if c != "" {
			t.Fatalf("initial_comment[%d] = %q, want empty", i, c)
		}
	}
}

func TestPinSlackSendExtractedImage_DataURLErrors(t *testing.T) {
	srv, capture := newSlackPinServer(t)
	a := newSlackPinAdapter(srv.URL)
	err := a.sendExtractedImage(context.Background(), "C1", "", ExtractedImage{Kind: "data_url", Data: "data:image/png;base64"})
	if err == nil || err.Error() != "invalid data URL" {
		t.Fatalf("no-comma err = %v, want exact %q", err, "invalid data URL")
	}
	err = a.sendExtractedImage(context.Background(), "C1", "", ExtractedImage{Kind: "data_url", Data: "data:image/png;base64,!!not-base64!!"})
	if err == nil || !strings.HasPrefix(err.Error(), "invalid base64 in data URL: ") {
		t.Fatalf("bad-base64 err = %v, want prefix %q", err, "invalid base64 in data URL: ")
	}
	paths, _, _, _, _, _ := capture.snapshot()
	if len(paths) != 0 {
		t.Fatalf("error paths must not hit network, got %v", paths)
	}
}

func TestPinSlackSendExtractedImage_LocalUpload(t *testing.T) {
	srv, capture := newSlackPinServer(t)
	a := newSlackPinAdapter(srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "snap-207.png")
	if err := os.WriteFile(path, []byte("local-bytes"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := a.sendExtractedImage(context.Background(), "C77", "T1", ExtractedImage{Kind: "url", Data: path}); err != nil {
		t.Fatalf("local url: %v", err)
	}
	paths, channels, threadTS, _, _, files := capture.snapshot()
	if len(paths) != 1 || paths[0] != "/files.upload" {
		t.Fatalf("paths = %v, want [/files.upload]", paths)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if files[0].filename != "snap-207.png" || string(files[0].data) != "local-bytes" {
		t.Fatalf("local pin: filename=%q data=%q", files[0].filename, files[0].data)
	}
	if channels[0] != "C77" || threadTS[0] != "T1" {
		t.Fatalf("channels=%q threadTS=%q", channels[0], threadTS[0])
	}
}

func TestPinSlackSendExtractedImage_LocalReadError(t *testing.T) {
	srv, capture := newSlackPinServer(t)
	a := newSlackPinAdapter(srv.URL)
	err := a.sendExtractedImage(context.Background(), "C1", "", ExtractedImage{Kind: "url", Data: filepath.Join(t.TempDir(), "missing.png")})
	if err == nil || !strings.HasPrefix(err.Error(), "read local image: ") {
		t.Fatalf("err = %v, want prefix %q", err, "read local image: ")
	}
	paths, _, _, _, _, _ := capture.snapshot()
	if len(paths) != 0 {
		t.Fatalf("read error must not hit network, got %v", paths)
	}
}

func TestPinSlackSendExtractedImage_RemoteDownload(t *testing.T) {
	uploadSrv, capture := newSlackPinServer(t)
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("remote-bytes"))
	}))
	defer dlSrv.Close()
	a := newSlackPinAdapter(uploadSrv.URL)
	if err := a.sendExtractedImage(context.Background(), "C9", "", ExtractedImage{Kind: "url", Data: dlSrv.URL + "/photo.png"}); err != nil {
		t.Fatalf("remote url: %v", err)
	}
	paths, channels, _, _, texts, files := capture.snapshot()
	if len(paths) != 1 || paths[0] != "/files.upload" {
		t.Fatalf("paths = %v, want [/files.upload]", paths)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if files[0].filename != "photo.png" || string(files[0].data) != "remote-bytes" {
		t.Fatalf("remote pin: filename=%q data=%q", files[0].filename, files[0].data)
	}
	if channels[0] != "C9" || len(texts) != 0 {
		t.Fatalf("channels=%q texts=%v", channels[0], texts)
	}
}

func TestPinSlackSendExtractedImage_RemoteFallbackToText(t *testing.T) {
	uploadSrv, capture := newSlackPinServer(t)
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer dlSrv.Close()
	a := newSlackPinAdapter(uploadSrv.URL)
	imgURL := dlSrv.URL + "/broken.gif"
	if err := a.sendExtractedImage(context.Background(), "C5", "T2", ExtractedImage{Kind: "url", Data: imgURL}); err != nil {
		t.Fatalf("fallback path: %v", err)
	}
	paths, channels, _, _, texts, files := capture.snapshot()
	if len(paths) != 1 || paths[0] != "/chat.postMessage" {
		t.Fatalf("paths = %v, want [/chat.postMessage]", paths)
	}
	if len(texts) != 1 || texts[0] != imgURL {
		t.Fatalf("texts = %v, want [%q]", texts, imgURL)
	}
	if channels[0] != "C5" || len(files) != 0 {
		t.Fatalf("channels=%q files=%d", channels[0], len(files))
	}
}
