package stt

// #2055 regression: STT audio reads were bounded by the IMAGE attachment
// ceiling (imagepkg.MaxSize, 20MB) since #1893 - a 25MB voice message
// failed deterministically with an opaque "read STT audio file" error.
// Audio now has its own 100MB bound and the failure message names the
// size and the limit.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAudioFile(t *testing.T, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audio.mp3")
	buf := make([]byte, 1024*1024)
	if err := os.WriteFile(p, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(int64(size-1024*1024), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(buf); err != nil {
		t.Fatal(err)
	}
	f.Close()
	fi, _ := os.Stat(p)
	if int(fi.Size()) != size {
		t.Fatalf("setup: wrote %d bytes, want %d", fi.Size(), size)
	}
	return p
}

// A 25MB file (over the old 20MB image ceiling, under the new audio one)
// must pass the read gate and reach the transcription endpoint.
func TestTranscribeAcceptsAudioAboveImageCeiling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer srv.Close()

	audio := writeAudioFile(t, 25*1024*1024)
	c := NewOpenAICompatible(srv.URL, "k", "whisper-1", "openai")
	res, err := c.Transcribe(context.Background(), Request{Path: audio})
	if err != nil {
		t.Fatalf("25MB audio must transcribe (old 20MB image ceiling regression): %v", err)
	}
	if res.Text != "ok" {
		t.Fatalf("unexpected transcript: %+v", res)
	}
}

// A file above MaxAudioSize fails with an error naming size and limit.
func TestTranscribeRejectsAudioAboveAudioLimit(t *testing.T) {
	audio := writeAudioFile(t, MaxAudioSize+1024*1024)
	c := NewOpenAICompatible("http://127.0.0.1:1", "k", "whisper-1", "openai")
	_, err := c.Transcribe(context.Background(), Request{Path: audio})
	if err == nil {
		t.Fatal("oversized audio must fail")
	}
	if !strings.Contains(err.Error(), "exceeds limit") || !strings.Contains(err.Error(), fmt.Sprint(int64(MaxAudioSize))) {
		t.Fatalf("error must name size and limit, got: %v", err)
	}
}
