package lanchat

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIssue1583_BodyCap pins #1583-A: authenticated-but-oversized POST
// bodies are rejected by MaxBytesReader before entering memory or the
// synchronous persistMessage disk write. The body is VALID JSON up to
// the cap (a long string value) - MaxBytesReader fires only once the
// decoder crosses the limit, so garbage bodies would fail as syntax
// errors first.
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
