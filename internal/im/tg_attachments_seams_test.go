package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"

	imstt "github.com/topcheer/ggcode/internal/im/stt"
)

// The tests in this file pin the observable behavior of tgAdapter's
// processAttachments pipeline (photo / voice / document) before and after its
// behavior-preserving decomposition into per-media-kind seams. Gate order,
// attachment field values, skip semantics, and voiceText propagation are all
// locked here.

// stubVoiceTranscriber is a minimal imstt.Transcriber used to drive the voice
// branch of processAttachments without an external STT provider.
type stubVoiceTranscriber struct{ text string }

func (s stubVoiceTranscriber) Transcribe(context.Context, imstt.Request) (imstt.Result, error) {
	return imstt.Result{Text: s.text}, nil
}

// tgMediaFile is one downloadable Telegram file served by tgMediaMux. A file
// named "broken" responds with HTTP 500 so download failures can be pinned.
type tgMediaFile struct {
	name        string
	contentType string
	data        []byte
}

// tgMediaMux mimics api.telegram.org: POST .../getFile resolves
// file_id -> file_path, GET /file/bot<token>/<path> serves the bytes.
// Matching is suffix/prefix based so the bot token may be arbitrary.
func tgMediaMux(files map[string]tgMediaFile) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			var body struct {
				FileID string `json:"file_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f, ok := files[body.FileID]
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"result": map[string]any{"file_path": f.name},
			})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/file/") {
			base := path.Base(r.URL.Path)
			for _, f := range files {
				if f.name != base {
					continue
				}
				if f.name == "broken" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", f.contentType)
				_, _ = w.Write(f.data)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func newTGMediaServer(t *testing.T, files map[string]tgMediaFile) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(tgMediaMux(files))
	t.Cleanup(srv.Close)
	return srv
}

func newTGAttachmentsAdapter(apiBase string, stt imstt.Transcriber) *tgAdapter {
	return &tgAdapter{name: "t", botToken: "T", apiBase: apiBase, httpClient: &http.Client{}, stt: stt}
}

func cleanupAttachmentPaths(t *testing.T, attachments []Attachment) {
	t.Helper()
	for _, att := range attachments {
		if att.Path != "" {
			_ = os.Remove(att.Path)
		}
	}
}

// TestTGProcessAttachmentsGateOrderAndPayloads pins the happy path for all
// three media kinds in one message: gate order photo -> voice -> document,
// largest-photo selection (last entry), MIME sniffing/fallbacks, the pinned
// voice.ogg/audio/ogg naming, transcript trimming, and voiceText propagation.
func TestTGProcessAttachmentsGateOrderAndPayloads(t *testing.T) {
	photoBig := []byte("fakepng-bytes-not-decodable")
	voiceData := []byte("RIFFwavdata")
	docData := []byte("doc-bytes")
	files := map[string]tgMediaFile{
		"photo-small": {name: "small.jpg", contentType: "image/jpeg", data: []byte("small")},
		"photo-big":   {name: "big.bin", contentType: "image/png", data: photoBig},
		"voice-wav":   {name: "v.wav", contentType: "audio/wav", data: voiceData},
		"doc-1":       {name: "notes.txt", contentType: "application/octet-stream", data: docData},
	}

	// Recording wrapper: log every getFile file_id, then delegate to the
	// standard media mux.
	var requested []string
	root := http.NewServeMux()
	root.HandleFunc("/botT/getFile", func(w http.ResponseWriter, r *http.Request) {
		// Read the body once for recording, then restore it so the
		// delegating mux can decode the file_id itself.
		bodyBytes, _ := io.ReadAll(r.Body)
		var probe struct {
			FileID string `json:"file_id"`
		}
		_ = json.Unmarshal(bodyBytes, &probe)
		requested = append(requested, probe.FileID)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		tgMediaMux(files).ServeHTTP(w, r)
	})
	root.Handle("/file/", tgMediaMux(files))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	a := newTGAttachmentsAdapter(srv.URL, stubVoiceTranscriber{text: "  hello voice  "})
	msg := map[string]any{
		"photo": []any{
			map[string]any{"file_id": "photo-small"},
			map[string]any{"file_id": "photo-big"},
		},
		"voice":    map[string]any{"file_id": "voice-wav"},
		"document": map[string]any{"file_id": "doc-1", "file_name": "notes.txt", "mime_type": "text/plain"},
	}

	attachments, voiceText := a.processAttachments(context.Background(), msg)
	defer cleanupAttachmentPaths(t, attachments)

	if voiceText != "hello voice" {
		t.Fatalf("voiceText = %q, want %q (trimmed transcript)", voiceText, "hello voice")
	}
	if len(attachments) != 3 {
		t.Fatalf("got %d attachments, want 3 (photo, voice, document): %+v", len(attachments), attachments)
	}
	photo, voice, doc := attachments[0], attachments[1], attachments[2]

	if photo.Kind != AttachmentImage || photo.Name != "photo.jpg" || photo.MIME != "image/png" {
		t.Errorf("photo = %+v, want Kind=AttachmentImage Name=photo.jpg MIME=image/png (server CT kept: payload not decodable)", photo)
	}
	if photo.DataBase64 != base64.StdEncoding.EncodeToString(photoBig) {
		t.Errorf("photo.DataBase64 mismatch: want encoding of %d-byte payload", len(photoBig))
	}
	if photo.Path == "" {
		t.Errorf("photo.Path must point at the cached file, got empty")
	}

	if voice.Kind != AttachmentVoice || voice.Name != "voice.ogg" || voice.MIME != "audio/ogg" || voice.Transcript != "hello voice" {
		t.Errorf("voice = %+v, want Kind=AttachmentVoice Name=voice.ogg MIME=audio/ogg Transcript=%q", voice, "hello voice")
	}

	if doc.Kind != AttachmentFile || doc.Name != "notes.txt" || doc.MIME != "text/plain" || doc.Path == "" {
		t.Errorf("doc = %+v, want Kind=AttachmentFile Name=notes.txt MIME=text/plain (declared mime wins over CT)", doc)
	}

	// Largest-photo selection: only the last (largest) entry is resolved via
	// getFile; the smaller entry must never be requested.
	bigCount := 0
	for _, id := range requested {
		if id == "photo-big" {
			bigCount++
		}
		if id == "photo-small" {
			t.Errorf("getFile requested %q; the smaller photo entry must never be fetched", id)
		}
	}
	if bigCount != 1 {
		t.Errorf("getFile requests = %v, want photo-big requested exactly once", requested)
	}
}

// TestTGProcessAttachmentsDocumentMIMEFallback pins the document MIME
// resolution: declared mime_type wins; otherwise the download response
// Content-Type is used.
func TestTGProcessAttachmentsDocumentMIMEFallback(t *testing.T) {
	files := map[string]tgMediaFile{
		"doc-2": {name: "raw.bin", contentType: "application/x-custom", data: []byte("raw-bytes")},
	}
	srv := newTGMediaServer(t, files)
	a := newTGAttachmentsAdapter(srv.URL, nil)
	msg := map[string]any{
		"document": map[string]any{"file_id": "doc-2", "file_name": "raw.bin"},
	}
	attachments, voiceText := a.processAttachments(context.Background(), msg)
	defer cleanupAttachmentPaths(t, attachments)
	if voiceText != "" {
		t.Fatalf("voiceText = %q, want empty", voiceText)
	}
	if len(attachments) != 1 {
		t.Fatalf("got %d attachments, want 1: %+v", len(attachments), attachments)
	}
	if attachments[0].MIME != "application/x-custom" {
		t.Errorf("doc MIME = %q, want response Content-Type fallback %q", attachments[0].MIME, "application/x-custom")
	}
}

// TestTGProcessAttachmentsSkipPaths pins every silent-skip gate: no
// attachment, no error surfaced, no voiceText.
func TestTGProcessAttachmentsSkipPaths(t *testing.T) {
	files := map[string]tgMediaFile{
		"voice-wav": {name: "v.wav", contentType: "audio/wav", data: []byte("RIFFwavdata")},
		"broken":    {name: "broken", contentType: "text/plain", data: []byte("never served")},
		"empty":     {name: "e.bin", contentType: "application/octet-stream", data: nil},
	}
	cases := []struct {
		name string
		stt  imstt.Transcriber
		msg  map[string]any
	}{
		{"photo empty file_id", nil, map[string]any{"photo": []any{map[string]any{"file_id": ""}}}},
		{"photo wrong shape", nil, map[string]any{"photo": []any{"not-a-map"}}},
		{"photo not a list", nil, map[string]any{"photo": "nope"}},
		{"photo download error", nil, map[string]any{"photo": []any{map[string]any{"file_id": "missing"}}}},
		{"photo download 500", nil, map[string]any{"photo": []any{map[string]any{"file_id": "broken"}}}},
		{"photo empty body", nil, map[string]any{"photo": []any{map[string]any{"file_id": "empty"}}}},
		{"voice no file_id", stubVoiceTranscriber{text: "x"}, map[string]any{"voice": map[string]any{"file_id": ""}}},
		{"voice nil stt", nil, map[string]any{"voice": map[string]any{"file_id": "voice-wav"}}},
		{"voice empty transcript", stubVoiceTranscriber{text: ""}, map[string]any{"voice": map[string]any{"file_id": "voice-wav"}}},
		{"voice wrong shape", stubVoiceTranscriber{text: "x"}, map[string]any{"voice": "nope"}},
		{"document no file_id", nil, map[string]any{"document": map[string]any{"file_name": "f.txt"}}},
		{"document download error", nil, map[string]any{"document": map[string]any{"file_id": "missing", "file_name": "f.txt"}}},
		{"document wrong shape", nil, map[string]any{"document": "nope"}},
		{"no media at all", nil, map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTGMediaServer(t, files)
			a := newTGAttachmentsAdapter(srv.URL, tc.stt)
			attachments, voiceText := a.processAttachments(context.Background(), tc.msg)
			defer cleanupAttachmentPaths(t, attachments)
			if voiceText != "" {
				t.Errorf("voiceText = %q, want empty", voiceText)
			}
			if len(attachments) != 0 {
				t.Errorf("got %d attachments, want 0: %+v", len(attachments), attachments)
			}
		})
	}
}
