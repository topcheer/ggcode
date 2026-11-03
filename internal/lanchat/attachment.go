package lanchat

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/topcheer/ggcode/internal/safego"
)

const maxAttachmentSize = 50 * 1024 * 1024

const attachmentTTL = 30 * time.Minute

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

func NewAttachmentManager() *AttachmentManager {
	am := &AttachmentManager{
		pending: make(map[string]*pendingAttachment),
		stopCh:  make(chan struct{}),
	}
	safego.Go("lanchat.cleanupLoop", func() { am.cleanupLoop() })
	return am
}

func (am *AttachmentManager) Stop() {
	close(am.stopCh)
}

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

var inlineSafeMIMEs = map[string]bool{
	"text/plain":       true,
	"text/markdown":    true,
	"text/csv":         true,
	"application/json": true,
	"image/png":        true,
	"image/jpeg":       true,
	"image/gif":        true,
	"image/webp":       true,
	"application/pdf":  true,
}

func sanitizeAttachmentName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
}

func (am *AttachmentManager) HandleAttachmentDownload(w http.ResponseWriter, r *http.Request) {

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

	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := sanitizeAttachmentName(att.name)
	if inlineSafeMIMEs[att.mimeType] {
		w.Header().Set("Content-Type", att.mimeType)
		w.Header().Set("Content-Disposition", "inline; filename=\""+name+"\"")
	} else {

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	}
	w.Write(att.data)
}
