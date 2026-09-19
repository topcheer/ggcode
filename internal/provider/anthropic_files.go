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

// Anthropic Files API integration (GA since 2025, no beta header).
//
// The agent re-sends the full conversation on every turn, so a large image
// attachment is re-inlined as base64 in every request payload. Images at or
// above filesUploadThresholdBytes are uploaded once to /v1/files and then
// referenced by file_id (ImageBlockParam source type "file"), which:
//   - removes the recurring per-turn payload cost for repeated attachments;
//   - lifts Anthropic's 5 MB base64 inline cap (Files API allows 500 MB), so
//     huge screenshots no longer hard-fail the request with a 400.
//
// The uploader is per-provider and in-memory: file_ids are valid for the
// lifetime of the file on Anthropic storage, so a session-scoped cache keyed
// by content hash uploads each unique image exactly once. Failures degrade
// gracefully to inline base64 — the feature is strictly additive.

const (
	// filesUploadThresholdBytes is the minimum decoded image size (2 MiB)
	// worth an upload round-trip. Smaller images inline as base64 exactly
	// as before, so short conversations see no behavior change.
	filesUploadThresholdBytes = 2 << 20
)

// fileUploader resolves image content to Anthropic file_ids, uploading on
// first sight and caching by content hash. All methods are nil-receiver safe:
// a nil *fileUploader means "feature disabled", and callers get a false ok.
type fileUploader struct {
	client  *anthropic.Client
	enabled bool

	mu             sync.Mutex
	cache          map[string]string    // sha256 hex -> file_id
	failed         map[string]time.Time // sha256 hex -> transient failure time; retried after filesRetryTTL
	endpointBroken bool                 // endpoint answered non-retryably: stop trying entirely
	uploads        int
}

// filesRetryTTL is how long a transient upload failure (429/5xx/network)
// suppresses further upload attempts for that one content hash (#2568).
// After it elapses the next turn retries with a fresh attempt, matching the
// documented "may retry later" contract. Package-level var so tests can
// shorten it.
var filesRetryTTL = 2 * time.Minute

func newFileUploader(client *anthropic.Client, baseURL string) *fileUploader {
	return &fileUploader{
		client:  client,
		enabled: filesAPIAllowed(baseURL),
		cache:   map[string]string{},
		failed:  map[string]time.Time{},
	}
}

// filesAPIAllowed decides whether the Files API may be attempted. It is on by
// default for api.anthropic.com (the SDK default when baseURL is empty) and
// for any *.anthropic.com gateway, and off for third-party base URLs that
// typically proxy only /v1/messages. GGCODE_FILES_API=1 force-enables it for
// gateways known to forward /v1/files; GGCODE_FILES_API=0 disables everywhere.
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

// resolve returns the file_id for the given image bytes, uploading them on
// first sight. ok=false means the caller should fall back to inline base64.
func (u *fileUploader) resolve(ctx context.Context, mime string, data []byte) (fileID string, ok bool) {
	if u == nil || u.client == nil || !u.enabled || u.endpointBroken || len(data) == 0 {
		return "", false
	}
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])

	u.mu.Lock()
	if id, hit := u.cache[key]; hit {
		u.mu.Unlock()
		return id, true
	}
	if t, hit := u.failed[key]; hit {
		if time.Since(t) < filesRetryTTL {
			u.mu.Unlock()
			return "", false
		}
		// Expired transient failure (#2568): retry with a fresh attempt
		// instead of poisoning this hash for the provider lifetime.
		delete(u.failed, key)
	}
	u.mu.Unlock()

	id, err := u.upload(ctx, mime, data, key)
	if err != nil {
		u.noteFailure(key, err)
		return "", false
	}

	u.mu.Lock()
	u.cache[key] = id
	delete(u.failed, key) // stale transient marker, if any, must not survive a success
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

// noteFailure classifies an upload error. Non-retryable 4xx (except 429)
// means the endpoint does not implement the Files API — disable the uploader
// for the provider lifetime instead of re-attempting every turn. Transient
// failures (network, 429, 5xx) poison only this content hash for
// filesRetryTTL; once the TTL elapses, resolve retries with a fresh attempt
// (#2568 — the old never-expiring marker made "may retry later" unreachable).
func (u *fileUploader) noteFailure(key string, err error) {
	permanent := false
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch sc := apiErr.StatusCode; {
		case sc == 429:
			// rate limited: transient
		case sc >= 500:
			// server side: transient
		default:
			permanent = true
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if permanent {
		u.endpointBroken = true
		debug.Log("anthropic-files", "Files API unavailable on this endpoint (%v); disabling for provider lifetime", err)
		return
	}
	u.failed[key] = time.Now()
	debug.Log("anthropic-files", "Files upload failed (transient): %v; falling back to base64 for this image (retry in %s)", err, filesRetryTTL)
}

// filesExtForMIME maps the image MIME types Anthropic accepts to file
// extensions for the upload part filename (purely cosmetic in the part name).
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

// filesImageSource converts a base64 image into a file_id-referenced image
// source when the payload is large enough to be worth the upload, falling
// back to inline base64 otherwise. Malformed base64 never blocks: the caller
// keeps the original string.
func (p *AnthropicProvider) filesImageSource(ctx context.Context, mime, b64 string) (anthropic.ImageBlockParamSourceUnion, bool) {
	// Cheap pre-check: base64 is ~4/3 of raw bytes, so anything shorter than
	// the threshold cannot reach it — skip the decode entirely.
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

// imageContentBlock renders an image content block, preferring a file_id
// reference for large payloads and always falling back to inline base64.
func (p *AnthropicProvider) imageContentBlock(ctx context.Context, mime, b64 string) anthropic.ContentBlockParamUnion {
	if src, ok := p.filesImageSource(ctx, mime, b64); ok {
		return anthropic.ContentBlockParamUnion{OfImage: &anthropic.ImageBlockParam{Source: src}}
	}
	return anthropic.NewImageBlockBase64(mime, b64)
}
