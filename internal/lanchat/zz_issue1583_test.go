package lanchat

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssue1583_BodyCap(t *testing.T) {
	store := NewStore(t.TempDir())
	h := NewHub("self-node", "cli", "http://127.0.0.1:1", "community", store, WorkspaceMeta{})
	defer h.Close()

	huge := []byte(`{"content":"` + strings.Repeat("x", 2<<20) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/lanchat/message", bytes.NewReader(huge))
	rec := httptest.NewRecorder()
	h.handleReceiveMessage(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body must 413, got %d", rec.Code)
	}
}
