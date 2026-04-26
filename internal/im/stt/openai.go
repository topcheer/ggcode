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
)

type OpenAICompatible struct {
	baseURL    string
	apiKey     string
	model      string
	provider   string
	httpClient *http.Client
}

func NewOpenAICompatible(baseURL, apiKey, model, provider string) *OpenAICompatible {
	return &OpenAICompatible{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		model:      strings.TrimSpace(model),
		provider:   strings.TrimSpace(provider),
		httpClient: &http.Client{Timeout: 60 * time.Second},
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
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return Result{}, fmt.Errorf("read STT audio file: %w", err)
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

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("decode STT response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("STT API error [%d]", resp.StatusCode)
	}
	return Result{
		Text:     strings.TrimSpace(payload.Text),
		Provider: t.provider,
		Model:    t.model,
	}, nil
}
