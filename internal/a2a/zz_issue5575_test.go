package a2a

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- fake introspector: zero LLM by construction, counts calls ---

type fakeIntrospector struct {
	calls int
	snap  *RuntimeSnapshotV2
	err   error
}

func (f *fakeIntrospector) RuntimeSnapshot(mode string) (*RuntimeSnapshotV2, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.snap != nil {
		cp := *f.snap
		return &cp, nil
	}
	return &RuntimeSnapshotV2{
		Busy:   &RuntimeBusy{Busy: true, BusySince: "2026-11-13T04:00:00Z", InFlightTool: "run_command", InFlightArgs: "api_key=sk-live-abcdef123456 build ./..."},
		Todos:  []RuntimeTodo{{ID: "1", Subject: "ship #5575", Status: "in_progress"}},
		Recent: []RuntimeToolTrace{{TS: "t1", Tool: "grep", Summary: "token=ghp_secret123 found", DurationMs: 12}},
	}, nil
}

// --- domain key ---

func TestIssue5575_DomainKeyGeneratedOncePerGitDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	k1, err := LoadOrCreateDomainKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 {
		t.Fatalf("key len = %d, want 32", len(k1))
	}
	fi, err := os.Stat(filepath.Join(dir, ".git", DomainKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key perm = %v, want 0600", fi.Mode().Perm())
	}
	k2, err := LoadOrCreateDomainKey(dir)
	if err != nil || string(k1) != string(k2) {
		t.Fatalf("key not stable: %v", err)
	}
}

func TestIssue5575_WorktreePointerSharesFamilyKey(t *testing.T) {
	main := t.TempDir()
	os.MkdirAll(filepath.Join(main, ".git"), 0o755)
	kMain, err := LoadOrCreateDomainKey(main)
	if err != nil {
		t.Fatal(err)
	}
	// linked worktree: .git is a pointer file
	wt := t.TempDir()
	realGit := filepath.Join(main, ".git", "worktrees", "wt1")
	if err := os.MkdirAll(realGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+realGit), 0o644); err != nil {
		t.Fatal(err)
	}
	kWt, err := LoadOrCreateDomainKey(wt)
	if err != nil {
		t.Fatal(err)
	}
	if string(kMain) != string(kWt) {
		t.Fatal("worktree must resolve to the shared family key")
	}
}

func TestIssue5575_CrossMachineDifferentKeyRejected(t *testing.T) {
	kA := []byte("0123456789abcdef0123456789abcdef")
	kB := []byte("fedcba9876543210fedcba9876543210")
	sig := func(k []byte) string {
		m := hmac.New(sha256.New, k)
		m.Write([]byte("n1|runtime/snapshot"))
		return hex.EncodeToString(m.Sum(nil))
	}
	if !VerifyIntrospectionProof(kA, "runtime/snapshot", "n1", sig(kA)) {
		t.Fatal("same-key proof must pass")
	}
	if VerifyIntrospectionProof(kB, "runtime/snapshot", "n1", sig(kA)) {
		t.Fatal("different .git key must be rejected (cross-machine isolation)")
	}
	if VerifyIntrospectionProof(kA, "runtime/snapshot", "", sig(kA)) {
		t.Fatal("missing nonce must fail")
	}
}

// --- wire-level: flag both states ---

func newIntrospectionTestServer(t *testing.T, mode string, key []byte, inst RuntimeIntrospector) *httptest.Server {
	t.Helper()
	srv := NewServer(ServerConfig{
		Port:              0,
		Instance:          "test",
		IntrospectionMode: mode,
		Introspector:      inst,
		DomainKey:         key,
	}, NewTaskHandler(".", nil, nil))
	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)
	return ts
}

func introspectionCall(t *testing.T, ts *httptest.Server, key []byte, method string) (*http.Response, map[string]interface{}) {
	t.Helper()
	nonce := "n-" + method
	m := hmac.New(sha256.New, key)
	m.Write([]byte(nonce + "|" + method))
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]interface{}{},
	})
	req, _ := http.NewRequest("POST", ts.URL, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-A2A-Introspection-Nonce", nonce)
	req.Header.Set("X-A2A-Introspection-Signature", hex.EncodeToString(m.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestIssue5575_OffStateMethodNotFoundByteIdentical(t *testing.T) {
	key, _ := LoadOrCreateDomainKey(mkGit(t))
	ts := newIntrospectionTestServer(t, IntrospectionOff, key, &fakeIntrospector{})
	resp, out := introspectionCall(t, ts, key, "runtime/snapshot")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	e, ok := out["error"].(map[string]interface{})
	if !ok || e["code"].(float64) != -32601 || e["message"] != "Method not found" {
		t.Fatalf("off-state must return the exact legacy not-found error, got %v", out)
	}
}

func TestIssue5575_OnStateSnapshotWithRedaction(t *testing.T) {
	key, _ := LoadOrCreateDomainKey(mkGit(t))
	fake := &fakeIntrospector{}
	ts := newIntrospectionTestServer(t, IntrospectionFull, key, fake)
	resp, out := introspectionCall(t, ts, key, "runtime/snapshot")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if out["error"] != nil {
		t.Fatalf("unexpected error: %v", out["error"])
	}
	if fake.calls != 1 {
		t.Fatalf("introspector calls = %d, want 1", fake.calls)
	}
	b, _ := json.Marshal(out)
	s := string(b)
	if strings.Contains(s, "sk-live-abcdef") || strings.Contains(s, "ghp_secret123") {
		t.Fatalf("secret leaked into snapshot: %s", s)
	}
	if !strings.Contains(s, "[REDACTED]") {
		t.Fatalf("redaction marker missing")
	}
	if !strings.Contains(s, `"schema":"v2"`) {
		t.Fatalf("schema v2 missing")
	}
}

func TestIssue5575_WrongProofDenied(t *testing.T) {
	key, _ := LoadOrCreateDomainKey(mkGit(t))
	ts := newIntrospectionTestServer(t, IntrospectionFull, key, &fakeIntrospector{})
	_, out := introspectionCall(t, ts, []byte("0000000000000000000000000000000"), "runtime/snapshot")
	e, ok := out["error"].(map[string]interface{})
	if !ok || e["code"].(float64) != -32001 {
		t.Fatalf("wrong key must be denied with -32001, got %v", out)
	}
}

func TestIssue5575_SnapshotUnder50msZeroLLM(t *testing.T) {
	key, _ := LoadOrCreateDomainKey(mkGit(t))
	ts := newIntrospectionTestServer(t, IntrospectionFull, key, &fakeIntrospector{})
	start := time.Now()
	resp, out := introspectionCall(t, ts, key, "runtime/snapshot")
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusOK || out["error"] != nil {
		t.Fatalf("call failed: %v", out)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("snapshot took %v, want <50ms", elapsed)
	}
}

// --- WiringPins (static): additive-only contract ---

func TestIssue5575_WiringPinsRuntimeGateBeforeSwitch(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, pin := range []string{
		`strings.HasPrefix(req.Method, "runtime/") && s.introspectionMode != IntrospectionOff && s.introspector != nil`,
		`VerifyIntrospectionProof(s.domainKey, req.Method, nonce, sig)`,
		`writeRPCError(w, req.ID, ErrIntrospectionDenied)`,
	} {
		if !strings.Contains(s, pin) {
			t.Fatalf("wiring pin missing: %q", pin)
		}
	}
	// legacy default arm unchanged
	if !strings.Contains(s, "default:\n\t\twriteRPCError(w, req.ID, ErrMethodNotFound)") {
		t.Fatal("legacy not-found default arm must stay byte-identical")
	}
}

func mkGit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
