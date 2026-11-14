package a2a

// Issue #5575 (P0) regression battery:
//   - flag off: runtime/* answers Method not found exactly like today;
//   - flag on: same-key snapshot served with zero LLM, wrong-domain key and
//     stale nonces rejected, card capability gated, secrets redacted,
//     worktree family shares the domain key.

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSnapshotSource struct {
	calls int
	snap  RuntimeSnapshot
}

func (f *fakeSnapshotSource) RuntimeSnapshot(mode string) (RuntimeSnapshot, error) {
	f.calls++
	out := f.snap
	out.Mode = mode
	return out, nil
}

func introspectTestWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return ws
}

func introspectRPC(t *testing.T, srv *Server, method string, key []byte, nonceTime time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	if key != nil {
		nonce, proof := BuildIntrospectionProof(key, method, nonceTime)
		req.Header.Set(IntrospectNonceHeader, nonce)
		req.Header.Set(IntrospectProofHeader, proof)
	}
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rpc response: %v (body=%s)", err, w.Body.String())
	}
	return w, resp
}

func rpcErrCode(t *testing.T, resp map[string]any) int {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp)
	}
	f, _ := e["code"].(float64)
	return int(f)
}

// Flag off (default): runtime/* must hit the untouched Method-not-found path.
func TestIntrospectionDisabledByDefault(t *testing.T) {
	ws := introspectTestWorkspace(t)
	srv := NewServer(ServerConfig{Port: 0}, NewTaskHandler(ws, nil, nil))
	_, resp := introspectRPC(t, srv, MethodRuntimeSnapshot, nil, time.Now())
	if got := rpcErrCode(t, resp); got != ErrMethodNotFound.Code {
		t.Fatalf("disabled introspection: code=%v want %v", got, ErrMethodNotFound.Code)
	}
}

// Flag on + same domain key: snapshot served, source consulted (zero LLM).
func TestIntrospectionSnapshotSameKey(t *testing.T) {
	ws := introspectTestWorkspace(t)
	key, _, err := LoadOrCreateDomainKey(ws)
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeSnapshotSource{snap: RuntimeSnapshot{
		Conversation: []IntrospectConversationTurn{{Role: "user", Text: "run deploy with sk-abcdef1234567890abcdef1234567890 now"}},
		Recent:       []IntrospectActivity{{TS: time.Now(), Tool: "run_command", Summary: "deploy ok"}},
		Busy:         &IntrospectBusy{Busy: true, Spinner: "Deploying"},
	}}
	srv := NewServer(ServerConfig{Port: 0, IntrospectionMode: IntrospectionModeFull, IntrospectionSource: src}, NewTaskHandler(ws, nil, nil))
	start := time.Now()
	_, resp := introspectRPC(t, srv, MethodRuntimeSnapshot, key, time.Now())
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("snapshot took %v, want <50ms", elapsed)
	}
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("same-key snapshot errored: %v", resp)
	}
	if src.calls != 1 {
		t.Fatalf("source calls=%d want 1", src.calls)
	}
	body, _ := json.Marshal(resp["result"])
	if strings.Contains(string(body), "sk-abcdef1234567890") {
		t.Fatalf("secret leaked into snapshot: %s", body)
	}
	var snap RuntimeSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Mode != IntrospectionModeFull {
		t.Fatalf("mode=%q want full", snap.Mode)
	}
	if len(snap.Recent) != 1 {
		t.Fatalf("full mode must keep recent ring, got %d", len(snap.Recent))
	}
}

// metadata mode: reduced disclosure - recent dropped, excerpts shorter.
func TestIntrospectionMetadataModeReduces(t *testing.T) {
	ws := introspectTestWorkspace(t)
	key, _, _ := LoadOrCreateDomainKey(ws)
	long := strings.Repeat("x", 300)
	src := &fakeSnapshotSource{snap: RuntimeSnapshot{
		Conversation: []IntrospectConversationTurn{{Role: "user", Text: long}},
		Recent:       []IntrospectActivity{{Tool: "run_command"}},
	}}
	srv := NewServer(ServerConfig{Port: 0, IntrospectionMode: IntrospectionModeMetadata, IntrospectionSource: src}, NewTaskHandler(ws, nil, nil))
	_, resp := introspectRPC(t, srv, MethodRuntimeSnapshot, key, time.Now())
	body, _ := json.Marshal(resp["result"])
	var snap RuntimeSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Recent) != 0 {
		t.Fatalf("metadata mode must drop recent, got %d", len(snap.Recent))
	}
	if n := len(snap.Conversation[0].Text); n > introspectClampMetadata+3 {
		t.Fatalf("metadata excerpt len=%d want <=%d+ellipsis", n, introspectClampMetadata)
	}
}

// Different .git (different machine) must be rejected.
func TestIntrospectionWrongDomainKeyRejected(t *testing.T) {
	ws := introspectTestWorkspace(t)
	if _, _, err := LoadOrCreateDomainKey(ws); err != nil {
		t.Fatal(err)
	}
	foreign := make([]byte, domainKeyLength)
	for i := range foreign {
		foreign[i] = byte(i + 1)
	}
	srv := NewServer(ServerConfig{Port: 0, IntrospectionMode: IntrospectionModeFull}, NewTaskHandler(ws, nil, nil))
	_, resp := introspectRPC(t, srv, MethodRuntimeSnapshot, foreign, time.Now())
	if got := rpcErrCode(t, resp); got != ErrAuthRequired.Code {
		t.Fatalf("foreign key: code=%v want %v", got, ErrAuthRequired.Code)
	}
}

// Stale nonce (outside freshness window) must be rejected.
func TestIntrospectionStaleNonceRejected(t *testing.T) {
	ws := introspectTestWorkspace(t)
	key, _, _ := LoadOrCreateDomainKey(ws)
	srv := NewServer(ServerConfig{Port: 0, IntrospectionMode: IntrospectionModeFull}, NewTaskHandler(ws, nil, nil))
	_, resp := introspectRPC(t, srv, MethodRuntimeSnapshot, key, time.Now().Add(-2*introspectNonceWindow))
	if got := rpcErrCode(t, resp); got != ErrAuthRequired.Code {
		t.Fatalf("stale nonce: code=%v want %v", got, ErrAuthRequired.Code)
	}
}

// Unknown runtime/* methods stay Method not found even when enabled.
func TestIntrospectionUnknownRuntimeMethod(t *testing.T) {
	ws := introspectTestWorkspace(t)
	key, _, _ := LoadOrCreateDomainKey(ws)
	srv := NewServer(ServerConfig{Port: 0, IntrospectionMode: IntrospectionModeFull}, NewTaskHandler(ws, nil, nil))
	_, resp := introspectRPC(t, srv, "runtime/whatever", key, time.Now())
	if got := rpcErrCode(t, resp); got != ErrMethodNotFound.Code {
		t.Fatalf("unknown runtime method: code=%v want %v", got, ErrMethodNotFound.Code)
	}
}

// Main checkout and linked worktrees resolve to the same domain key.
func TestDomainKeyWorktreeFamilySharesKey(t *testing.T) {
	root := t.TempDir()
	mainGit := filepath.Join(root, "repo", ".git")
	if err := os.MkdirAll(filepath.Join(mainGit, "worktrees", "wt1"), 0o755); err != nil {
		t.Fatal(err)
	}
	wsMain := filepath.Join(root, "repo")
	if _, err := os.Stat(filepath.Join(wsMain, "config")); err != nil { // no-op keep
		_ = err
	}
	keyMain, pathMain, err := LoadOrCreateDomainKey(wsMain)
	if err != nil {
		t.Fatal(err)
	}
	// Linked worktree: .git file pointing at <main>/.git/worktrees/wt1
	wsWT := filepath.Join(root, "wtree")
	if err := os.MkdirAll(wsWT, 0o755); err != nil {
		t.Fatal(err)
	}
	pointer := "gitdir: " + filepath.Join(mainGit, "worktrees", "wt1")
	if err := os.WriteFile(filepath.Join(wsWT, ".git"), []byte(pointer), 0o644); err != nil {
		t.Fatal(err)
	}
	keyWT, pathWT, err := LoadOrCreateDomainKey(wsWT)
	if err != nil {
		t.Fatal(err)
	}
	if string(keyMain) != string(keyWT) {
		t.Fatalf("worktree key differs from main: %x vs %x", keyMain, keyWT)
	}
	if pathMain != pathWT {
		t.Fatalf("key paths differ: %s vs %s", pathMain, pathWT)
	}
	if fi, err := os.Stat(pathMain); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key perms want 0600, got %v err=%v", fi.Mode().Perm(), err)
	}
}

// Card capability is advertised only when the gate is armed.
func TestIntrospectionCardCapabilityGated(t *testing.T) {
	ws := introspectTestWorkspace(t)
	off := NewServer(ServerConfig{Port: 0}, NewTaskHandler(ws, nil, nil))
	if off.card.Capabilities.RuntimeIntrospection {
		t.Fatal("flag off must not advertise runtimeIntrospection")
	}
	raw, _ := json.Marshal(off.card)
	if strings.Contains(string(raw), "runtimeIntrospection") {
		t.Fatalf("off-state card JSON mentions runtimeIntrospection: %s", raw)
	}
	if _, _, err := LoadOrCreateDomainKey(ws); err != nil {
		t.Fatal(err)
	}
	on := NewServer(ServerConfig{Port: 0, IntrospectionMode: IntrospectionModeFull}, NewTaskHandler(ws, nil, nil))
	if !on.card.Capabilities.RuntimeIntrospection {
		t.Fatal("flag on must advertise runtimeIntrospection")
	}
}

// Proof round-trip unit: fresh ok, tampered method rejected.
func TestIntrospectionProofRoundTrip(t *testing.T) {
	key := make([]byte, domainKeyLength)
	for i := range key {
		key[i] = byte(3 * i)
	}
	nonce, proof := BuildIntrospectionProof(key, MethodRuntimeSnapshot, time.Now())
	if err := verifyIntrospectionProof(key, MethodRuntimeSnapshot, nonce, proof, time.Now()); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if err := verifyIntrospectionProof(key, "runtime/other", nonce, proof, time.Now()); err == nil {
		t.Fatal("proof bound to a different method must fail")
	}
}
