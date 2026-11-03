package lanchat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newIssue987Hub(t *testing.T) *Hub {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	return NewHub("self-node", "tui", "http://127.0.0.1:1", "", store, WorkspaceMeta{})
}

type receiptRecorder struct {
	mu       sync.Mutex
	receipts []Receipt
}

func (r *receiptRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/lanchat/receipt" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var rc Receipt
	if err := json.NewDecoder(req.Body).Decode(&rc); err != nil {
		http.Error(w, "bad receipt", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.receipts = append(r.receipts, rc)
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (r *receiptRecorder) find(messageID, status string) *Receipt {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.receipts {
		if r.receipts[i].MessageID == messageID && r.receipts[i].Status == status {
			cp := r.receipts[i]
			return &cp
		}
	}
	return nil
}

func agentDirectMsg(id, fromRole string) Message {
	return Message{
		ID:         id,
		FromNodeID: "peer-987",
		FromNick:   "peerHuman",
		FromRole:   fromRole,
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "run the tests please",
		Timestamp:  time.Now().UnixMilli(),
	}
}

func registerPeer(h *Hub, srv *httptest.Server) {
	h.HandlePresence(Participant{
		NodeID:    "peer-987",
		HumanNick: "peerHuman",
		AgentNick: "peerHuman_agent",
		Endpoint:  srv.URL,
		Online:    true,
		LastSeen:  time.Now().Unix(),
	})
}

func TestIssue987ManualApprovalCompletedReceipt(t *testing.T) {
	rec := &receiptRecorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	h := newIssue987Hub(t)
	registerPeer(h, srv)

	msg := agentDirectMsg("msg-987-approve", RoleHuman)
	h.HandleIncomingMessage(msg)

	pending := h.PendingApprovals()
	if len(pending) != 1 || pending[0].Message.ID != msg.ID {
		t.Fatalf("expected 1 pending approval for %s, got %+v", msg.ID, pending)
	}

	approved, err := h.ApproveMessage(msg.ID)
	if err != nil || approved == nil {
		t.Fatalf("ApproveMessage(%s) failed: %v", msg.ID, err)
	}

	h.NotifyAgentComplete(msg.ID)

	deadline := time.Now().Add(2 * time.Second)
	for rec.find(msg.ID, StatusCompleted) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := rec.find(msg.ID, StatusCompleted); got == nil {
		t.Fatalf("no completed receipt sent for manually approved @agent message %s (NotifyAgentComplete lookup failed)", msg.ID)
	}
}

func TestIssue987DuplicateMessageDeduped(t *testing.T) {
	h := newIssue987Hub(t)

	var mu sync.Mutex
	injected := 0
	h.SetOnAutoApprove(func(Message) {
		mu.Lock()
		injected++
		mu.Unlock()
	})

	msg := agentDirectMsg("msg-987-dup", RoleAgent)
	h.HandleIncomingMessage(msg)

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := injected
		mu.Unlock()
		if n == 1 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	first := injected
	mu.Unlock()
	if first != 1 {
		t.Fatalf("first delivery should inject exactly once, got %d", first)
	}

	h.HandleIncomingMessage(msg)
	h.HandleIncomingMessage(msg)

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	final := injected
	mu.Unlock()
	if final != 1 {
		t.Fatalf("duplicate deliveries must not re-inject into agent loop: injected %d times, want 1", final)
	}
}

func TestIssue987PendingApprovalTTLCleanup(t *testing.T) {
	rec := &receiptRecorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	h := newIssue987Hub(t)
	registerPeer(h, srv)

	h.mu.Lock()
	h.pendingApproval = append(h.pendingApproval, PendingAgentMsg{
		Message:  agentDirectMsg("msg-987-stale", RoleHuman),
		Received: time.Now().Add(-pendingApprovalTTL - time.Minute),
	})
	h.mu.Unlock()

	fresh := agentDirectMsg("msg-987-fresh", RoleHuman)
	h.HandleIncomingMessage(fresh)

	pending := h.PendingApprovals()
	if len(pending) != 1 || pending[0].Message.ID != fresh.ID {
		t.Fatalf("expected only the fresh pending approval to survive, got %+v", pending)
	}

	deadline := time.Now().Add(2 * time.Second)
	for rec.find("msg-987-stale", StatusRejected) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.find("msg-987-stale", StatusRejected) == nil {
		t.Fatal("expired pending approval should emit a rejected receipt to the sender")
	}
}

func TestIssue987PendingApprovalCapacityCap(t *testing.T) {
	rec := &receiptRecorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	h := newIssue987Hub(t)
	registerPeer(h, srv)

	h.mu.Lock()
	for i := 0; i < maxPendingApprovals; i++ {
		h.pendingApproval = append(h.pendingApproval, PendingAgentMsg{
			Message:  agentDirectMsg("msg-987-cap", RoleHuman),
			Received: time.Now(),
		})
	}
	h.mu.Unlock()

	fresh := agentDirectMsg("msg-987-newest", RoleHuman)
	h.HandleIncomingMessage(fresh)

	pending := h.PendingApprovals()
	if len(pending) != maxPendingApprovals {
		t.Fatalf("pending approval queue must be capped at %d, got %d", maxPendingApprovals, len(pending))
	}
	found := false
	for _, p := range pending {
		if p.Message.ID == fresh.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("newest pending approval must survive the cap eviction")
	}
}
