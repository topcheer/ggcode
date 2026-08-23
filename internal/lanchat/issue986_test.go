package lanchat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// --- Issue #986, Problem 1: communityKey must not bypass a configured key ---

func TestIssue986CommunityKeyRejectedWhenCustomKeyConfigured(t *testing.T) {
	called := false
	handler := AuthMiddleware("my-strong-secret", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/lanchat/messages", nil)
	req.Header.Set("X-API-Key", communityKey)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("community key with custom key configured: got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if called {
		t.Error("handler must not be invoked when the community key is rejected")
	}

	// The configured key itself still works.
	req2 := httptest.NewRequest(http.MethodGet, "/lanchat/messages", nil)
	req2.Header.Set("X-API-Key", "my-strong-secret")
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)
	if rec2.Code != http.StatusOK || !called {
		t.Errorf("configured key rejected: status %d called=%v", rec2.Code, called)
	}
}

func TestIssue986CommunityKeyAcceptedInZeroConfig(t *testing.T) {
	handler := AuthMiddleware("", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/lanchat/messages", nil)
	req.Header.Set("X-API-Key", communityKey)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("zero-config community key: got status %d, want %d", rec.Code, http.StatusOK)
	}

	// A hub whose configured key IS the community key (EffectiveAPIKey default
	// on the receiving side) must behave like zero-config.
	handler2 := AuthMiddleware(communityKey, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec2 := httptest.NewRecorder()
	handler2(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("community key vs community-key hub: got status %d, want %d", rec2.Code, http.StatusOK)
	}
}

func TestIssue986WrongKeyStillRejected(t *testing.T) {
	handler := AuthMiddleware("", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/lanchat/messages", nil)
	req.Header.Set("X-API-Key", "something-else")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong key in zero-config: got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// --- Issue #986, Problem 1: FromRole self-report must not bypass the gate ---

func newIssue986Hub(t *testing.T, mode string) *Hub {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	return NewHub("self-node", mode, "http://127.0.0.1:1", communityKey, store, WorkspaceMeta{})
}

func TestIssue986DaemonHumanDMRequiresApproval(t *testing.T) {
	h := newIssue986Hub(t, "daemon")
	h.HandleIncomingMessage(Message{
		ID:         "m1",
		FromNodeID: "peer-node",
		FromRole:   RoleHuman,
		FromNick:   "PeerHuman",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "run this",
	})
	pending := h.PendingApprovals()
	if len(pending) != 1 {
		t.Fatalf("daemon human DM should queue for manual approval, got %d pending", len(pending))
	}
	if pending[0].Message.ID != "m1" {
		t.Errorf("pending message ID = %q, want m1", pending[0].Message.ID)
	}
}

func TestIssue986AgentDMAutoApprovedByDefault(t *testing.T) {
	h := newIssue986Hub(t, "cli")
	h.HandleIncomingMessage(Message{
		ID:         "m2",
		FromNodeID: "peer-node",
		FromRole:   RoleAgent,
		FromNick:   "PeerAgent",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "collab task",
	})
	if got := len(h.PendingApprovals()); got != 0 {
		t.Errorf("agent-role DM should auto-approve by default, got %d pending", got)
	}
}

func TestIssue986RequireApprovalForAgentsOptOut(t *testing.T) {
	h := newIssue986Hub(t, "cli")
	h.SetRequireAgentApproval(true)
	h.HandleIncomingMessage(Message{
		ID:         "m3",
		FromNodeID: "peer-node",
		FromRole:   RoleAgent,
		FromNick:   "PeerAgent",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "collab task",
	})
	if got := len(h.PendingApprovals()); got != 1 {
		t.Errorf("with require_approval_for_agents, agent DM must queue for approval, got %d pending", got)
	}
}

func TestIssue986AlwaysPolicyStillAutoApprovesHuman(t *testing.T) {
	h := newIssue986Hub(t, "cli")
	h.SetApprovalPolicy("PeerHuman", "always")
	h.HandleIncomingMessage(Message{
		ID:         "m4",
		FromNodeID: "peer-node",
		FromRole:   RoleHuman,
		FromNick:   "PeerHuman",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "run this",
	})
	if got := len(h.PendingApprovals()); got != 0 {
		t.Errorf("explicit always policy should still auto-approve, got %d pending", got)
	}
}

// --- Issue #986, Problem 2: attachment download MIME whitelist ---

func serveIssue986Attachment(t *testing.T, mimeType, name string) *httptest.ResponseRecorder {
	t.Helper()
	am := NewAttachmentManager()
	defer am.Stop()
	att := am.Store(name, []byte("payload"), mimeType)
	req := httptest.NewRequest(http.MethodGet, "/lanchat/attach/"+att.ID, nil)
	rec := httptest.NewRecorder()
	am.HandleAttachmentDownload(rec, req)
	return rec
}

func TestIssue986AttachmentWhitelistedMIMEInline(t *testing.T) {
	for _, mt := range []string{"text/plain", "text/markdown", "image/png", "image/jpeg", "image/gif", "image/webp", "application/json", "application/pdf"} {
		rec := serveIssue986Attachment(t, mt, "file.txt")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", mt, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != mt {
			t.Errorf("%s: Content-Type = %q, want preserved whitelisted type", mt, got)
		}
		cd := rec.Header().Get("Content-Disposition")
		if len(cd) < 6 || cd[:6] != "inline" {
			t.Errorf("%s: Content-Disposition = %q, want inline", mt, cd)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", mt, got)
		}
	}
}

func TestIssue986AttachmentUnsafeMIMEForcedDownload(t *testing.T) {
	for _, mt := range []string{"text/html", "image/svg+xml", "application/x-httpd-php", "text/javascript"} {
		rec := serveIssue986Attachment(t, mt, "evil.html")
		if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("%s: Content-Type = %q, want application/octet-stream", mt, got)
		}
		cd := rec.Header().Get("Content-Disposition")
		if len(cd) < 10 || cd[:10] != "attachment" {
			t.Errorf("%s: Content-Disposition = %q, want forced attachment", mt, cd)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", mt, got)
		}
	}
}

func TestIssue986AttachmentFilenameQuoteEscaping(t *testing.T) {
	rec := serveIssue986Attachment(t, "text/plain", `weird"; evil=.txt`)
	cd := rec.Header().Get("Content-Disposition")
	if cd != `inline; filename="weird_; evil=.txt"` {
		t.Errorf("Content-Disposition = %q, want quotes/backslash sanitized", cd)
	}
}

func TestIssue986SanitizeAttachmentName(t *testing.T) {
	got := sanitizeAttachmentName("a\x00b\"c\\d\ne")
	want := "a_b_c_d_e"
	if got != want {
		t.Errorf("sanitizeAttachmentName = %q, want %q", got, want)
	}
}
