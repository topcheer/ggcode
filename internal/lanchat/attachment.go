package lanchat

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/topcheer/ggcode/internal/safego"
)

// maxAttachmentSize is the maximum allowed attachment size (50MB).
const maxAttachmentSize = 50 * 1024 * 1024

// attachmentTTL is how long an attachment stays available for download.
const attachmentTTL = 30 * time.Minute

// AttachmentManager stores attachments in memory with TTL-based cleanup.
type AttachmentManager struct {
	mu      sync.RWMutex
	pending map[string]*pendingAttachment
	stopCh  chan struct{}
}

type pendingAttachment struct {
	data     []byte
	name     string
	mimeType string
	created  time.Time
}

// NewAttachmentManager creates a new attachment manager and starts the
// background cleanup goroutine.
func NewAttachmentManager() *AttachmentManager {
	am := &AttachmentManager{
		pending: make(map[string]*pendingAttachment),
		stopCh:  make(chan struct{}),
	}
	safego.Go("lanchat.cleanupLoop", func() { am.cleanupLoop() })
	return am
}

// Stop halts the cleanup goroutine.
func (am *AttachmentManager) Stop() {
	close(am.stopCh)
}

// Store saves an attachment and returns its metadata.
func (am *AttachmentManager) Store(name string, data []byte, mimeType string) Attachment {
	id := uuid.NewString()
	am.mu.Lock()
	am.pending[id] = &pendingAttachment{
		data:     data,
		name:     name,
		mimeType: mimeType,
		created:  time.Now(),
	}
	am.mu.Unlock()

	return Attachment{
		ID:       id,
		Name:     name,
		Size:     int64(len(data)),
		MIMEType: mimeType,
	}
}

// Get retrieves an attachment by ID. Returns nil if not found or expired.
func (am *AttachmentManager) Get(id string) *pendingAttachment {
	am.mu.RLock()
	defer am.mu.RUnlock()
	a, ok := am.pending[id]
	if !ok {
		return nil
	}
	if time.Since(a.created) > attachmentTTL {
		return nil
	}
	return a
}

// SetAttachmentURL populates the URL field for an attachment based on the
// sender's endpoint.
func SetAttachmentURL(endpoint string, att *Attachment) {
	att.URL = strings.TrimRight(endpoint, "/") + "/lanchat/attach/" + att.ID
}

func (am *AttachmentManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			am.cleanup()
		case <-am.stopCh:
			return
		}
	}
}

func (am *AttachmentManager) cleanup() {
	am.mu.Lock()
	defer am.mu.Unlock()
	for id, a := range am.pending {
		if time.Since(a.created) > attachmentTTL {
			delete(am.pending, id)
		}
	}
}

// HandleAttachmentDownload serves an attachment by ID via HTTP.
func (am *AttachmentManager) HandleAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /lanchat/attach/{id}
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/lanchat/attach/"), "/")
	id := pathParts[0]
	if id == "" {
		http.Error(w, "missing attachment ID", http.StatusBadRequest)
		return
	}

	att := am.Get(id)
	if att == nil {
		http.Error(w, "attachment not found or expired", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", att.mimeType)
	w.Header().Set("Content-Disposition", "inline; filename=\""+att.name+"\"")
	w.Write(att.data)
}

// DownloadAttachment fetches an attachment from a peer's URL.
func DownloadAttachment(url, apiKey string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", &AttachmentDownloadError{StatusCode: resp.StatusCode, URL: url}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxAttachmentSize {
		return nil, "", &AttachmentDownloadError{StatusCode: http.StatusRequestEntityTooLarge, URL: url}
	}

	// Guess MIME type from URL path extension if Content-Type is generic
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		ext := strings.ToLower(filepath.Ext(url))
		mimeType = mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	return data, mimeType, nil
}

// ReadFileForAttachment reads a local file and returns attachment metadata.
func ReadFileForAttachment(path string) (string, []byte, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, "", err
	}
	if info.Size() > maxAttachmentSize {
		return "", nil, "", &AttachmentDownloadError{StatusCode: http.StatusRequestEntityTooLarge, URL: path}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, "", err
	}

	name := filepath.Base(path)
	mimeType := mime.TypeByExtension(filepath.Ext(name))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	return name, data, mimeType, nil
}

// AttachmentDownloadError represents a failed attachment download.
type AttachmentDownloadError struct {
	StatusCode int
	URL        string
}

func (e *AttachmentDownloadError) Error() string {
	return strings.ToLower(http.StatusText(e.StatusCode))
}
