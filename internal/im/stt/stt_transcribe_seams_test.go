package stt

// Pin tests for the r190 Transcribe seam extraction. The orchestrator
// `Transcribe` was flattened into pure predicates / phase seams; these
// tests pin each seam's contract so any behavior drift (error wording,
// ordering, temp-file lifecycle, limit classification) fails loudly.
// Existing #2055 / #1560-D tests must keep passing untouched.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSTTUnconfigured(t *testing.T) {
	full := &OpenAICompatible{baseURL: "http://x", apiKey: "k", model: "m"}
	cases := []struct {
		name string
		t    *OpenAICompatible
		want bool
	}{
		{"nil receiver", nil, true},
		{"zero value", &OpenAICompatible{}, true},
		{"no baseURL", &OpenAICompatible{apiKey: "k", model: "m"}, true},
		{"no apiKey", &OpenAICompatible{baseURL: "http://x", model: "m"}, true},
		{"no model", &OpenAICompatible{baseURL: "http://x", apiKey: "k"}, true},
		{"full config", full, false},
	}
	for _, tc := range cases {
		if got := sttUnconfigured(tc.t); got != tc.want {
			t.Errorf("%s: sttUnconfigured=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTranscribeNilAndUnconfiguredGate(t *testing.T) {
	var nilT *OpenAICompatible
	if _, err := nilT.Transcribe(context.Background(), Request{Path: "x"}); err == nil || err.Error() != "STT is not configured" {
		t.Fatalf("nil transcriber must report exactly %q, got %v", "STT is not configured", err)
	}
	c := &OpenAICompatible{}
	if _, err := c.Transcribe(context.Background(), Request{Path: "x"}); err == nil || err.Error() != "STT is not configured" {
		t.Fatalf("zero-value transcriber must report exactly %q, got %v", "STT is not configured", err)
	}
}

func TestResolveSTTAudioSource(t *testing.T) {
	t.Run("explicit path is trimmed, no temp file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "a.mp3")
		path, cleanup, err := resolveSTTAudioSource(Request{Path: "  " + p + "  "})
		if err != nil {
			t.Fatal(err)
		}
		if path != p {
			t.Fatalf("path %q, want %q", path, p)
		}
		cleanup() // must be a safe no-op
	})

	t.Run("base64 data materializes temp file with req.Name extension", func(t *testing.T) {
		payload := []byte("fake-audio-bytes")
		path, cleanup, err := resolveSTTAudioSource(Request{
			Name:       "clip.mp3",
			DataBase64: "  " + base64.StdEncoding.EncodeToString(payload) + "  ",
		})
		if err != nil {
			t.Fatal(err)
		}
		// os.CreateTemp replaces the pattern's * with a random string.
		if !strings.HasPrefix(filepath.Base(path), "ggcode-stt-") || !strings.HasSuffix(path, ".mp3") {
			t.Fatalf("temp file name %q must keep ggcode-stt-* prefix and .mp3 suffix", filepath.Base(path))
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("temp content %q, want %q", got, payload)
		}
		cleanup()
		if _, serr := os.Stat(path); !os.IsNotExist(serr) {
			t.Fatalf("cleanup must remove temp file %q", path)
		}
	})

	t.Run("invalid base64 wraps decode error", func(t *testing.T) {
		_, _, err := resolveSTTAudioSource(Request{DataBase64: "!!!not-base64!!!"})
		if err == nil || !strings.HasPrefix(err.Error(), "decode STT audio data:") {
			t.Fatalf("want decode STT audio data error, got %v", err)
		}
		var corr base64.CorruptInputError
		if !errors.As(err, &corr) {
			t.Fatalf("must wrap base64.CorruptInputError, got %v", err)
		}
	})

	t.Run("empty path and empty data yields empty path, no error", func(t *testing.T) {
		path, cleanup, err := resolveSTTAudioSource(Request{})
		if err != nil {
			t.Fatal(err)
		}
		if path != "" {
			t.Fatalf("path %q, want empty", path)
		}
		cleanup()
	})
}

func TestClassifySTTAudioReadError(t *testing.T) {
	sentinel := errors.New("read limited")
	t.Run("existing file names size and limit", func(t *testing.T) {
		p := writeAudioFile(t, 3*1024*1024)
		err := classifySTTAudioReadError(p, sentinel)
		want := fmt.Sprintf("read STT audio file (size %d bytes exceeds limit %d bytes): read limited", 3*1024*1024, int64(MaxAudioSize))
		if err == nil || err.Error() != want {
			t.Fatalf("got %v, want %q", err, want)
		}
		if !errors.Is(err, sentinel) {
			t.Fatal("must wrap the underlying read error")
		}
	})
	t.Run("missing file names limit only", func(t *testing.T) {
		err := classifySTTAudioReadError(filepath.Join(t.TempDir(), "gone.mp3"), sentinel)
		want := fmt.Sprintf("read STT audio file (limit %d bytes): read limited", int64(MaxAudioSize))
		if err == nil || err.Error() != want {
			t.Fatalf("got %v, want %q", err, want)
		}
		if !errors.Is(err, sentinel) {
			t.Fatal("must wrap the underlying read error")
		}
	})
}

func TestBuildSTTMultipart(t *testing.T) {
	t.Run("assembles model field and file part", func(t *testing.T) {
		p := writeAudioFile(t, 1024*1024)
		body, writer, err := buildSTTMultipart(p, "whisper-1")
		if err != nil {
			t.Fatal(err)
		}
		ct := writer.FormDataContentType()
		if ct == "" {
			t.Fatal("writer must expose a multipart content type")
		}
		if !strings.Contains(ct, "multipart/form-data") {
			t.Fatalf("content type %q, want multipart/form-data", ct)
		}
		if !bytes.Contains(body.Bytes(), []byte(`name="model"`)) ||
			!bytes.Contains(body.Bytes(), []byte("whisper-1")) {
			t.Fatal("body must contain the model field")
		}
		if !bytes.Contains(body.Bytes(), []byte(`filename="audio.mp3"`)) {
			t.Fatalf("body must contain the audio.mp3 form file, got %d bytes", body.Len())
		}
	})

	t.Run("unreadable file surfaces open error", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope.mp3")
		_, _, err := buildSTTMultipart(missing, "whisper-1")
		if err == nil || !strings.HasPrefix(err.Error(), "read STT audio file:") {
			t.Fatalf("want read STT audio file open error, got %v", err)
		}
	})
}

func TestSendSTTRequestAndParseSTTResponse(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer auth, got %q", r.Header.Get("Authorization"))
		}
		if !strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			t.Errorf("path %q, want suffix /audio/transcriptions", r.URL.Path)
		}
		fmt.Fprint(w, `{"text":"  hello world  "}`)
	}))
	defer okSrv.Close()

	c := &OpenAICompatible{baseURL: okSrv.URL, apiKey: "test-key", model: "whisper-1", provider: "openai", httpClient: okSrv.Client()}
	resp, err := c.sendSTTRequest(context.Background(), "multipart/form-data; boundary=x", bytes.NewBufferString("payload"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := parseSTTResponse(resp, c.provider, c.model)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello world" || res.Provider != "openai" || res.Model != "whisper-1" {
		t.Fatalf("result %+v, want trimmed text with provider/model stamped", res)
	}

	t.Run("HTTP error status surfaces trimmed body", func(t *testing.T) {
		errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "  bad key  \n")
		}))
		defer errSrv.Close()
		c2 := &OpenAICompatible{baseURL: errSrv.URL, apiKey: "k", model: "m", httpClient: errSrv.Client()}
		resp2, err := c2.sendSTTRequest(context.Background(), "multipart/form-data; boundary=x", bytes.NewBufferString("p"))
		if err != nil {
			t.Fatal(err)
		}
		_, perr := parseSTTResponse(resp2, "openai", "m")
		resp2.Body.Close()
		want := "STT API error [401]: bad key"
		if perr == nil || perr.Error() != want {
			t.Fatalf("got %v, want %q", perr, want)
		}
	})

	t.Run("invalid JSON surfaces decode error", func(t *testing.T) {
		badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"text":`)
		}))
		defer badSrv.Close()
		c3 := &OpenAICompatible{baseURL: badSrv.URL, apiKey: "k", model: "m", httpClient: badSrv.Client()}
		resp3, err := c3.sendSTTRequest(context.Background(), "multipart/form-data; boundary=x", bytes.NewBufferString("p"))
		if err != nil {
			t.Fatal(err)
		}
		_, perr := parseSTTResponse(resp3, "openai", "m")
		resp3.Body.Close()
		if perr == nil || !strings.HasPrefix(perr.Error(), "decode STT response:") {
			t.Fatalf("want decode STT response error, got %v", perr)
		}
	})

	t.Run("unreachable endpoint surfaces send error", func(t *testing.T) {
		c4 := &OpenAICompatible{baseURL: "http://127.0.0.1:1", apiKey: "k", model: "m", httpClient: &http.Client{}}
		if _, err := c4.sendSTTRequest(context.Background(), "multipart/form-data; boundary=x", bytes.NewBufferString("p")); err == nil || !strings.HasPrefix(err.Error(), "send STT request:") {
			t.Fatalf("want send STT request error, got %v", err)
		}
	})
}
