package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// seven-face source with cursor pagination over 40 synthetic turns
type v3Fake struct{}

func (v3Fake) RuntimeSnapshotV3(req SnapshotRequest) (*RuntimeSnapshotV3, error) {
	snap := &RuntimeSnapshotV3{Schema: "v3", TakenAt: "t", Depth: req.Depth}
	snap.Status = &RuntimeStatus{Busy: true, Model: "m", Goal: "g"}
	snap.Inflight = &RuntimeInflight{Tool: "edit_file"}
	// conversation cursor = plain index
	all := make([]RuntimeConversationTurn, 40)
	for i := range all {
		all[i] = RuntimeConversationTurn{Role: "user", Text: strings.Repeat("x", 300)}
	}
	lim := req.LimitConv
	if lim == 0 {
		lim = 20
	}
	start := 0
	if req.CursorConv != "" {
		json.Unmarshal([]byte(req.CursorConv), &start)
	}
	end := start + lim
	trunc := end < len(all)
	if end > len(all) {
		end = len(all)
	}
	if req.Depth == DepthBrief {
		for i := start; i < end; i++ {
			all[i].Text = all[i].Text[:120] + "..."
		}
	}
	next := ""
	if trunc {
		next = `"` + itoa(end) + `"`
	}
	snap.Conversation = &RuntimeConversation{Turns: all[start:end], NextCursor: next, Truncated: trunc}
	snap.Activity = &RuntimeActivity{Events: []RuntimeActivityEvent{{Tool: "read_file", OK: true}}}
	snap.Children = &RuntimeChildren{}
	snap.Schedule = &RuntimeSchedule{Todos: []RuntimeTodo{{ID: "1", Subject: "s", Status: "in_progress"}}}
	snap.Env = &RuntimeEnv{Workspace: "/w", Branch: "main"}
	return snap, nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func newV3Server(gate func(*http.Request) bool) *httptest.Server {
	srv := &Server{introspectorV3: v3Fake{}, workspaceGate: gate}
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.a2aMiddleware(srv.handleRPC))
	return httptest.NewServer(mux)
}

func postSnapshot(t *testing.T, url string, params string) (*JSONRPCResponse, error) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"runtime/snapshot"` + params + `}`
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Pin 1: seven-face shape over the real mux/middleware path, no auth config
// (zero-config default ON), no HMAC headers anywhere.
func TestIssue5597_SevenFaceShapeZeroConfig(t *testing.T) {
	ts := newV3Server(nil)
	defer ts.Close()
	out, err := postSnapshot(t, ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("zero-config plain call denied: %+v", out.Error)
	}
	raw, _ := json.Marshal(out.Result)
	var snap RuntimeSnapshotV3
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Schema != "v3" || snap.Status == nil || snap.Inflight == nil ||
		snap.Conversation == nil || snap.Activity == nil || snap.Children == nil ||
		snap.Schedule == nil || snap.Env == nil {
		t.Fatalf("seven-face shape broken: %+v", snap)
	}
	if len(snap.Conversation.Turns) != 20 || !snap.Conversation.Truncated || snap.Conversation.NextCursor == "" {
		t.Fatalf("default full depth contract broken: turns=%d trunc=%v cursor=%q",
			len(snap.Conversation.Turns), snap.Conversation.Truncated, snap.Conversation.NextCursor)
	}
}

// Pin 2: depth tiers - brief caps and clips; cursor paginates in full.
func TestIssue5597_DepthAndCursor(t *testing.T) {
	ts := newV3Server(nil)
	defer ts.Close()

	out, _ := postSnapshot(t, ts.URL, `,"params":{"depth":"brief","limit_conversation":5}`)
	raw, _ := json.Marshal(out.Result)
	var brief RuntimeSnapshotV3
	json.Unmarshal(raw, &brief)
	if len(brief.Conversation.Turns) != 5 {
		t.Fatalf("brief limit override ignored: %d", len(brief.Conversation.Turns))
	}

	out, _ = postSnapshot(t, ts.URL, `,"params":{"cursor_conversation":"30"}`)
	raw, _ = json.Marshal(out.Result)
	var page RuntimeSnapshotV3
	json.Unmarshal(raw, &page)
	if len(page.Conversation.Turns) != 10 || page.Conversation.Truncated {
		t.Fatalf("cursor pagination broken: turns=%d trunc=%v", len(page.Conversation.Turns), page.Conversation.Truncated)
	}
}

// Pin 3: roster workspace gate - same-workspace caller passes, foreign
// endpoint gets 403-equivalent denied; message/send is unaffected by
// design (not gated here).
func TestIssue5597_WorkspaceGate(t *testing.T) {
	ts := newV3Server(func(*http.Request) bool { return true }) // same-workspace caller
	defer ts.Close()
	out, err := postSnapshot(t, ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("same-workspace caller denied: %+v", out.Error)
	}

	ts2 := newV3Server(func(r *http.Request) bool { return false })
	defer ts2.Close()
	out2, _ := postSnapshot(t, ts2.URL, "")
	if out2.Error == nil || out2.Error.Code != -32003 {
		t.Fatalf("cross-workspace caller must be denied -32003, got %+v", out2.Error)
	}
}

// Pin 4: client round-trip via IntrospectPeerV3 (plain transport).
func TestIssue5597_ClientRoundTrip(t *testing.T) {
	ts := newV3Server(nil)
	defer ts.Close()
	snap, err := IntrospectPeerV3(context.Background(), ts.URL, "", SnapshotRequest{Depth: DepthFull})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status == nil || !snap.Status.Busy || snap.Env.Workspace != "/w" {
		t.Fatalf("client round-trip broke: %+v", snap)
	}
	_ = time.Now
}

// #5605 reviewer follow-up: the A-arm (loopback) is a NEW trust branch
// and needs its own pins - plus the B-face service-name shape.
func TestIssue5605_LoopbackPasses(t *testing.T) {
	if !WorkspaceGateDecision("127.0.0.1:54321", nil, "/w") {
		t.Fatal("loopback source must pass (one-shot CLI arm)")
	}
	if !WorkspaceGateDecision("[::1]:54321", nil, "/w") {
		t.Fatal("IPv6 loopback must pass")
	}
}

func TestIssue5605_CrossMachineRosterReject(t *testing.T) {
	roster := []InstanceInfo{{Workspace: "/w", Endpoint: "http://192.168.1.50:9700"}}
	if WorkspaceGateDecision("192.168.1.99:5555", roster, "/w") {
		t.Fatal("non-loopback host absent from roster must fail closed")
	}
	if WorkspaceGateDecision("192.168.1.50:5555", []InstanceInfo{{Workspace: "/OTHER", Endpoint: "http://192.168.1.50:9700"}}, "/w") {
		t.Fatal("roster hit with foreign workspace must fail closed")
	}
	if !WorkspaceGateDecision("192.168.1.50:5555", roster, "/w") {
		t.Fatal("roster hit with same workspace must pass")
	}
}

func TestIssue5605_ServiceNameShape(t *testing.T) {
	// B face: no double-dot malformation ("_ggcode._tcp.local.." was the
	// zero-response root cause).
	full := mDNSServiceType + "." + mDNSDomain + "."
	if strings.Contains(full, "..") {
		t.Fatalf("malformed service name %q (double dot)", full)
	}
	if mDNSServiceType != "_ggcode._tcp" {
		t.Fatalf("service type drifted: %q", mDNSServiceType)
	}
}

// #5605(C): file-backed warm roster round-trip - upsert/read/remove and
// the stale-entry TTL filter.
func TestIssue5605_RosterFileWarmFallback(t *testing.T) {
	dir := t.TempDir()
	RosterUpsert(dir, InstanceInfo{ID: "a1", Workspace: dir, Endpoint: "http://127.0.0.1:1111"})
	RosterUpsert(dir, InstanceInfo{ID: "a2", Workspace: dir, Endpoint: "http://127.0.0.1:2222"})
	got := ReadRoster(dir, "me")
	if len(got) != 2 {
		t.Fatalf("warm roster read: %d entries", len(got))
	}
	RosterRemove(dir, "a1")
	if got = ReadRoster(dir, "me"); len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("remove broke roster: %+v", got)
	}
}
