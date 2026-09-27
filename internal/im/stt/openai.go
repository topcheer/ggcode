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

// sttUnconfigured reports whether the OpenAI-compatible STT endpoint is
// missing any required field. A nil transcriber counts as unconfigured.
func sttUnconfigured(t *OpenAICompatible) bool {
	return t == nil || t.baseURL == "" || t.apiKey == "" || t.model == ""
}

// resolveSTTAudioSource determines the audio input for a transcription:
// an explicit file path, or - when the path is empty and base64 audio
// data is supplied - a freshly written temp file. The returned cleanup
// removes that temp file and must be deferred by the caller; it is a
// no-op when no temp file was created.
func resolveSTTAudioSource(req Request) (audioPath string, cleanup func(), err error) {
	audioPath = strings.TrimSpace(req.Path)
	cleanup = func() {}
	if audioPath == "" && strings.TrimSpace(req.DataBase64) != "" {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.DataBase64))
		if err != nil {
			return "", cleanup, fmt.Errorf("decode STT audio data: %w", err)
		}
		tmpFile, err := os.CreateTemp("", "ggcode-stt-*"+filepath.Ext(req.Name))
		if err != nil {
			return "", cleanup, fmt.Errorf("create STT temp file: %w", err)
		}
		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			return "", cleanup, fmt.Errorf("write STT temp file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			return "", cleanup, fmt.Errorf("close STT temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		audioPath = tmpPath
		cleanup = func() { _ = os.Remove(tmpPath) }
	}
	return audioPath, cleanup, nil
}

// classifySTTAudioReadError renders a bounded-read failure (#1893/#2055).
// When the audio file still exists the error names its size and the
// ceiling; otherwise it names just the limit.
func classifySTTAudioReadError(audioPath string, err error) error {
	if fi, serr := os.Stat(audioPath); serr == nil {
		return fmt.Errorf("read STT audio file (size %d bytes exceeds limit %d bytes): %w", fi.Size(), int64(MaxAudioSize), err)
	}
	return fmt.Errorf("read STT audio file (limit %d bytes): %w", int64(MaxAudioSize), err)
}

// buildSTTMultipart assembles the audio/transcriptions form body: the
// model field plus the audio file part, read under the audio-specific
// size ceiling. The returned writer is needed afterwards for the
// multipart Content-Type header.
func buildSTTMultipart(audioPath, model string) (*bytes.Buffer, *multipart.Writer, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", model); err != nil {
		return nil, nil, fmt.Errorf("write STT model field: %w", err)
	}
	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, nil, fmt.Errorf("create STT form file: %w", err)
	}
	// #1893: same unbounded-read class as the image attachments - the
	// path can be externally provided, so bound it; #2055: with the
	// audio-appropriate ceiling, not the 20MB image one.
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read STT audio file: %w", err)
	}
	data, err := imagepkg.ReadLimited(f, MaxAudioSize)
	f.Close()
	if err != nil {
		return nil, nil, classifySTTAudioReadError(audioPath, err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, nil, fmt.Errorf("write STT audio file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, nil, fmt.Errorf("close STT multipart writer: %w", err)
	}
	return &body, writer, nil
}

// sendSTTRequest posts the multipart body to the audio/transcriptions
// endpoint. The response body is owned by the caller.
func (t *OpenAICompatible) sendSTTRequest(ctx context.Context, contentType string, body *bytes.Buffer) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/audio/transcriptions", body)
	if err != nil {
		return nil, fmt.Errorf("create STT request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+t.apiKey)
	httpReq.Header.Set("Content-Type", contentType)
	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send STT request: %w", err)
	}
	return resp, nil
}

// parseSTTResponse converts an API response into a Result. HTTP error
// statuses surface the trimmed response body; success decodes the JSON
// text payload.
func parseSTTResponse(resp *http.Response, provider, model string) (Result, error) {
	if resp.StatusCode >= 400 {
		errBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		return Result{}, fmt.Errorf("STT API error [%d]: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("decode STT response: %w", err)
	}
	return Result{
		Text:     strings.TrimSpace(payload.Text),
		Provider: provider,
		Model:    model,
	}, nil
}

func (t *OpenAICompatible) Transcribe(ctx context.Context, req Request) (Result, error) {
	if sttUnconfigured(t) {
		return Result{}, fmt.Errorf("STT is not configured")
	}
	audioPath, cleanup, err := resolveSTTAudioSource(req)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	if audioPath == "" {
		return Result{}, fmt.Errorf("STT audio path is empty")
	}
	body, writer, err := buildSTTMultipart(audioPath, t.model)
	if err != nil {
		return Result{}, err
	}
	resp, err := t.sendSTTRequest(ctx, writer.FormDataContentType(), body)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	return parseSTTResponse(resp, t.provider, t.model)
}
