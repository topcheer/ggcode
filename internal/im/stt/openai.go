package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	imagepkg "github.com/topcheer/ggcode/internal/image"

	"github.com/topcheer/ggcode/internal/util"
)

type OpenAICompatible struct {
	baseURL    string
	apiKey     string
	model      string
	provider   string
	httpClient *http.Client
}

// MaxAudioSize bounds STT audio uploads (100MB). #2055: the #1893 fix
// reused the IMAGE attachment ceiling (imagepkg.MaxSize, 20MB), which
// legitimate audio routinely exceeds - a 1-hour mp3 is ~55MB, and
// Telegram's ffmpeg conversion to uncompressed WAV expands files
// further (16kHz/16bit mono passes 20MB at ~10 minutes of speech).
// Audio keeps its own, larger bound instead of the unbounded pre-#1893
// read.
const MaxAudioSize = 100 * 1024 * 1024

func NewOpenAICompatible(baseURL, apiKey, model, provider string) *OpenAICompatible {
	return &OpenAICompatible{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:   strings.TrimSpace(apiKey),
		model:    strings.TrimSpace(model),
		provider: strings.TrimSpace(provider),
		// #2055: 60s was set when uploads were capped at the 20MB image
		// ceiling; a 100MB audio upload on a typical uplink takes minutes.
		httpClient: util.NewInsecureAwareClient(300 * time.Second),
	}
}

func (t *OpenAICompatible) Transcribe(ctx context.Context, req Request) (Result, error) {
	if t == nil || t.baseURL == "" || t.apiKey == "" || t.model == "" {
		return Result{}, fmt.Errorf("STT is not configured")
	}
	audioPath := strings.TrimSpace(req.Path)
	cleanup := func() {}
	if audioPath == "" && strings.TrimSpace(req.DataBase64) != "" {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.DataBase64))
		if err != nil {
			return Result{}, fmt.Errorf("decode STT audio data: %w", err)
		}
		tmpFile, err := os.CreateTemp("", "ggcode-stt-*"+filepath.Ext(req.Name))
		if err != nil {
			return Result{}, fmt.Errorf("create STT temp file: %w", err)
		}
		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			return Result{}, fmt.Errorf("write STT temp file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			return Result{}, fmt.Errorf("close STT temp file: %w", err)
		}
		audioPath = tmpFile.Name()
		cleanup = func() { _ = os.Remove(audioPath) }
	}
	defer cleanup()
	if audioPath == "" {
		return Result{}, fmt.Errorf("STT audio path is empty")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", t.model); err != nil {
		return Result{}, fmt.Errorf("write STT model field: %w", err)
	}
	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return Result{}, fmt.Errorf("create STT form file: %w", err)
	}
	// #1893: same unbounded-read class as the image attachments - the
	// path can be externally provided, so bound it; #2055: with the
	// audio-appropriate ceiling, not the 20MB image one.
	f, err := os.Open(audioPath)
	if err != nil {
		return Result{}, fmt.Errorf("read STT audio file: %w", err)
	}
	data, err := imagepkg.ReadLimited(f, MaxAudioSize)
	f.Close()
	if err != nil {
		if fi, serr := os.Stat(audioPath); serr == nil {
			return Result{}, fmt.Errorf("read STT audio file (size %d bytes exceeds limit %d bytes): %w", fi.Size(), int64(MaxAudioSize), err)
		}
		return Result{}, fmt.Errorf("read STT audio file (limit %d bytes): %w", int64(MaxAudioSize), err)
	}
	if _, err := part.Write(data); err != nil {
		return Result{}, fmt.Errorf("write STT audio file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return Result{}, fmt.Errorf("close STT multipart writer: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return Result{}, fmt.Errorf("create STT request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+t.apiKey)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("send STT request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		return Result{}, fmt.Errorf("STT API error [%d]: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("decode STT response: %w", err)
	}
	return Result{
		Text:     strings.TrimSpace(payload.Text),
		Provider: t.provider,
		Model:    t.model,
	}, nil
}
