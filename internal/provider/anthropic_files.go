package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	filesUploadThresholdBytes = 2 << 20
)

type fileUploader struct {
	client  *anthropic.Client
	enabled bool

	mu             sync.Mutex
	cache          map[string]filesCacheEntry
	failed         map[string]time.Time
	endpointBroken bool
	uploads        int
}

type filesCacheEntry struct {
	fileID     string
	uploadedAt time.Time
}

var filesRetryTTL = 2 * time.Minute

var filesCacheTTL = 24 * time.Hour

func newFileUploader(client *anthropic.Client, baseURL string) *fileUploader {
	return &fileUploader{
		client:  client,
		enabled: filesAPIAllowed(baseURL),
		cache:   map[string]filesCacheEntry{},
		failed:  map[string]time.Time{},
	}
}

func filesAPIAllowed(baseURL string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GGCODE_FILES_API"))) {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}
	if baseURL == "" {
		return true
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	return host == "api.anthropic.com" || strings.HasSuffix(host, ".anthropic.com")
}

func (u *fileUploader) resolve(ctx context.Context, mime string, data []byte) (fileID string, ok bool) {

	if u == nil || u.client == nil || !u.enabled || len(data) == 0 {
		return "", false
	}
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])

	u.mu.Lock()
	if u.endpointBroken {
		u.mu.Unlock()
		return "", false
	}
	if e, hit := u.cache[key]; hit {
		if time.Since(e.uploadedAt) < filesCacheTTL {
			u.mu.Unlock()
			return e.fileID, true
		}

		delete(u.cache, key)
	}
	if t, hit := u.failed[key]; hit {
		if time.Since(t) < filesRetryTTL {
			u.mu.Unlock()
			return "", false
		}

		delete(u.failed, key)
	}
	u.mu.Unlock()

	id, err := u.upload(ctx, mime, data, key)
	if err != nil {
		u.noteFailure(key, err)
		return "", false
	}

	u.mu.Lock()
	u.cache[key] = filesCacheEntry{fileID: id, uploadedAt: time.Now()}
	delete(u.failed, key)
	u.uploads++
	u.mu.Unlock()
	debug.Log("anthropic-files", "uploaded image to Files API: %d bytes %s -> %s (dedupe #%d)", len(data), mime, id, u.uploads)
	return id, true
}

func (u *fileUploader) upload(ctx context.Context, mime string, data []byte, key string) (string, error) {
	up, err := u.client.Files.Upload(ctx, anthropic.FileUploadParams{
		File: anthropic.File(bytes.NewReader(data), "ggcode-image-"+key[:12]+filesExtForMIME(mime), mime),
	})
	if err != nil {
		return "", err
	}
	if up.ID == "" {
		return "", errors.New("files upload returned empty file_id")
	}
	return up.ID, nil
}

func (u *fileUploader) noteFailure(key string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	permanent := false
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch sc := apiErr.StatusCode; {
		case sc == 404 || sc == 403:

			permanent = true
		case sc == 400 && u.uploads == 0:

			permanent = true
		}
	}
	if permanent {
		u.endpointBroken = true
		debug.Log("anthropic-files", "Files API unavailable on this endpoint (%v); disabling for provider lifetime", err)
		return
	}
	u.failed[key] = time.Now()
	debug.Log("anthropic-files", "Files upload failed (transient): %v; falling back to base64 for this image (retry in %s)", err, filesRetryTTL)
}

func filesExtForMIME(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func (p *AnthropicProvider) filesImageSource(ctx context.Context, mime, b64 string) (anthropic.ImageBlockParamSourceUnion, bool) {

	if p.files == nil || len(b64) < filesUploadThresholdBytes {
		return anthropic.ImageBlockParamSourceUnion{}, false
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(data) < filesUploadThresholdBytes {
		return anthropic.ImageBlockParamSourceUnion{}, false
	}
	id, ok := p.files.resolve(ctx, mime, data)
	if !ok {
		return anthropic.ImageBlockParamSourceUnion{}, false
	}
	return anthropic.ImageBlockParamSourceUnion{
		OfFile: &anthropic.FileImageSourceParam{FileID: id},
	}, true
}

func (p *AnthropicProvider) imageContentBlock(ctx context.Context, mime, b64 string) anthropic.ContentBlockParamUnion {
	if src, ok := p.filesImageSource(ctx, mime, b64); ok {
		return anthropic.ContentBlockParamUnion{OfImage: &anthropic.ImageBlockParam{Source: src}}
	}
	return anthropic.NewImageBlockBase64(mime, b64)
}
