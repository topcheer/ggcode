package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #5667: bind declared-workspace trust to a credential (deferred defect C
// of #5655). The workspace path string is public data; a matching
// declaration alone must not pass the introspection gate for non-loopback,
// non-roster callers. The credential is the workspace domain key (see
// workspace_key.go): advertised over the LAN discovery channel (mDNS TXT +
// roster file), proven via a stateless HMAC header.

func issue5667Key(seed byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// Acceptance 1 (decision function): matching workspace string but no valid
// workspace credential is DENIED on a non-loopback, non-roster path; a
// valid proof passes; the roster arm keeps working without any proof.
func TestIssue5667MatchingDeclarationRequiresCredential(t *testing.T) {
	key := issue5667Key(1)
	foreignKey := issue5667Key(9)
	now := time.Now()
	proof, err := BuildWorkspaceProof(key, MethodRuntimeSnapshot, now)
	if err != nil {
		t.Fatal(err)
	}
	rosterNone := []InstanceInfo{{Workspace: "/other", Endpoint: "http://192.168.31.50:9700"}}

	if WorkspaceGateDecisionWithDecl("192.168.31.99:5555", "/w", "", rosterNone, "/w", key, now) {
		t.Fatal("matching declaration WITHOUT proof must be denied on non-loopback non-roster path")
	}
	if WorkspaceGateDecisionWithDecl("192.168.31.99:5555", "/w", "v1 garbage", rosterNone, "/w", key, now) {
		t.Fatal("malformed proof must be denied")
	}
	if WorkspaceGateDecisionWithDecl("192.168.31.99:5555", "/w", proof, rosterNone, "/w", foreignKey, now) {
		t.Fatal("proof minted under a different key must be denied")
	}
	expired, _ := BuildWorkspaceProof(key, MethodRuntimeSnapshot, now.Add(-workspaceProofWindow-time.Minute))
	if WorkspaceGateDecisionWithDecl("192.168.31.99:5555", "/w", expired, rosterNone, "/w", key, now) {
		t.Fatal("expired proof must be denied")
	}
	future, _ := BuildWorkspaceProof(key, MethodRuntimeSnapshot, now.Add(workspaceProofWindow+time.Minute))
	if WorkspaceGateDecisionWithDecl("192.168.31.99:5555", "/w", future, rosterNone, "/w", key, now) {
		t.Fatal("future-dated proof must be denied")
	}
	if !WorkspaceGateDecisionWithDecl("192.168.31.99:5555", "/w", proof, rosterNone, "/w", key, now) {
		t.Fatal("matching declaration + valid proof must pass (cross-host same-workspace, #5655 field shape)")
	}

	// Old-peer compatibility: the roster reverse-lookup arm passes with no
	// declaration and no proof at all.
	rosterHit := []InstanceInfo{{Workspace: "/w", Endpoint: "http://192.168.31.99:9700"}}
	if !WorkspaceGateDecisionWithDecl("192.168.31.99:5555", "", "", rosterHit, "/w", nil, now) {
		t.Fatal("roster-matched host must keep passing without any credential (old-peer arm unchanged)")
	}
	// Loopback arm unchanged (no proof needed).
	if !WorkspaceGateDecisionWithDecl("127.0.0.1:5555", "/w", "", nil, "/w", nil, now) {
		t.Fatal("loopback arm must stay proof-free")
	}
}

// Proof primitive: build/verify round-trip, tamper resistance, window.
func TestIssue5667ProofBuildVerify(t *testing.T) {
	key := issue5667Key(2)
	now := time.Now()
	proof, err := BuildWorkspaceProof(key, MethodRuntimeSnapshot, now)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyWorkspaceProof(key, MethodRuntimeSnapshot, proof, now) {
		t.Fatal("freshly built proof must verify")
	}
	if VerifyWorkspaceProof(issue5667Key(3), MethodRuntimeSnapshot, proof, now) {
		t.Fatal("verification under a foreign key must fail")
	}
	if VerifyWorkspaceProof(key, "message/send", proof, now) {
		t.Fatal("proof is method-bound and must not verify for another method")
	}
	// Tamper each field (replacement char guaranteed different from the
	// original so the tamper is real).
	for i := 4; i <= 28; i += 8 {
		sub := byte('a')
		if proof[i] == 'a' {
			sub = 'b'
		}
		tampered := proof[:i] + string(sub) + proof[i+1:]
		if tampered == proof {
			t.Fatalf("tamper at offset %d was a no-op", i)
		}
		if VerifyWorkspaceProof(key, MethodRuntimeSnapshot, tampered, now) {
			t.Fatalf("tampered proof (offset %d) must fail", i)
		}
	}
	if VerifyWorkspaceProof(key, MethodRuntimeSnapshot, "", now) {
		t.Fatal("empty proof must fail")
	}
	if _, err := BuildWorkspaceProof(nil, MethodRuntimeSnapshot, now); err == nil {
		t.Fatal("building a proof without a key must error")
	}
}

// Domain key file placement: git family dir first (worktree pointer
// resolves to the shared main gitdir), .ggcode fallback for non-git.
func TestIssue5667DomainKeyFiles(t *testing.T) {
	base := t.TempDir()

	// Plain git workspace: key lives at .git/a2a.key.
	gitWS := filepath.Join(base, "gitws")
	if err := os.MkdirAll(filepath.Join(gitWS, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	k1, err := LoadOrCreateWorkspaceDomainKey(gitWS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(gitWS, ".git", DomainKeyFile)); err != nil {
		t.Fatalf("key file must live in the git dir: %v", err)
	}
	k2, err := LoadOrCreateWorkspaceDomainKey(gitWS)
	if err != nil || string(k1) != string(k2) {
		t.Fatal("key must be stable across loads")
	}
	fi, err := os.Stat(filepath.Join(gitWS, ".git", DomainKeyFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file must be 0600, got %v err=%v", fi.Mode().Perm(), err)
	}

	// Linked worktree: .git pointer file resolves up to the main gitdir so
	// the whole family shares one key.
	mainGit := filepath.Join(base, "main", ".git")
	if err := os.MkdirAll(mainGit, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	ptr := "gitdir: " + mainGit + "/worktrees/wt"
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(ptr), 0o644); err != nil {
		t.Fatal(err)
	}
	km, err := LoadOrCreateWorkspaceDomainKey(filepath.Join(base, "main"))
	if err != nil {
		t.Fatal(err)
	}
	kw, err := LoadOrCreateWorkspaceDomainKey(wt)
	if err != nil {
		t.Fatal(err)
	}
	if string(km) != string(kw) {
		t.Fatal("worktree family must share one domain key")
	}

	// Non-git workspace: .ggcode/a2a-domain.key fallback.
	plain := filepath.Join(base, "plainws")
	kp1, err := LoadOrCreateWorkspaceDomainKey(plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plain, ".ggcode", WorkspaceDomainKeyFile)); err != nil {
		t.Fatalf("non-git fallback key file missing: %v", err)
	}
	kp2, err := LoadOrCreateWorkspaceDomainKey(plain)
	if err != nil || string(kp1) != string(kp2) {
		t.Fatal("fallback key must be stable")
	}
}

// Negotiation channel: the key advertisement survives the mDNS TXT and
// roster-file wire forms unchanged (same-workspace peers resolve the peer
// key with zero manual config).
func TestIssue5667KeyAdvertisementRoundTrip(t *testing.T) {
	key := issue5667Key(5)
	info := InstanceInfo{
		ID: "i1", Workspace: "/w", Endpoint: "http://192.168.31.105:64932",
		Status: "ready", PID: 42, StartedAt: "t",
		DomainKeyID: WorkspaceDomainKeyID(key),
		DomainKey:   EncodeDomainKey(key),
	}
	txt := parseTXTFields(instanceTXT(info))
	if txt["wkid"] != info.DomainKeyID || txt["wkkey"] != info.DomainKey {
		t.Fatalf("TXT advertisement lost key fields: %v", txt)
	}

	// roster file round-trip
	dir := t.TempDir()
	RosterUpsert(dir, info)
	got := ReadRoster(dir, "me")
	if len(got) != 1 || got[0].DomainKeyID != info.DomainKeyID || got[0].DomainKey != info.DomainKey {
		t.Fatalf("roster file lost key advertisement: %+v", got)
	}

	// endpoint resolution decodes usable key bytes
	if k := DomainKeyForEndpoint(got, info.Endpoint); k == nil || string(k) != string(key) {
		t.Fatal("DomainKeyForEndpoint must decode the advertised key")
	}
	if k := DomainKeyForEndpoint(got, "http://192.168.31.105:1"); k != nil {
		t.Fatal("foreign endpoint must not resolve a key")
	}
	bad := []InstanceInfo{{Endpoint: info.Endpoint, DomainKey: "zz-not-hex"}}
	if k := DomainKeyForEndpoint(bad, info.Endpoint); k != nil {
		t.Fatal("malformed advertisement must be ignored")
	}
}

// Client face: IntrospectPeerV3 attaches the proof header when it holds
// the peer key, and stays silent when it does not.
func TestIssue5667ClientSendsProofHeader(t *testing.T) {
	key := issue5667Key(7)
	var captured map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = map[string]string{
			"ws":    r.Header.Get("X-Ggcode-Workspace"),
			"proof": r.Header.Get(WorkspaceProofHeader),
		}
		w.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{\"schema\":\"v3\"}}"))
	}))
	defer ts.Close()

	if _, err := IntrospectPeerV3(context.Background(), ts.URL, "", "/w", key, SnapshotRequest{Depth: DepthFull}); err != nil {
		t.Fatal(err)
	}
	if captured["ws"] != "/w" {
		t.Fatalf("workspace declaration header missing: %v", captured)
	}
	if !VerifyWorkspaceProof(key, MethodRuntimeSnapshot, captured["proof"], time.Now()) {
		t.Fatalf("proof header must verify under the peer key, got %q", captured["proof"])
	}

	captured = nil
	if _, err := IntrospectPeerV3(context.Background(), ts.URL, "", "/w", nil, SnapshotRequest{Depth: DepthFull}); err != nil {
		t.Fatal(err)
	}
	if captured["proof"] != "" {
		t.Fatal("no key -> no proof header must be sent")
	}
}

func issue5667Server(t *testing.T, apiKey string, key []byte) *Server {
	t.Helper()
	srv := &Server{introspectorV3: v3Fake{}, domainKey: key}
	if apiKey != "" {
		srv.apiKeys = []string{apiKey}
	}
	// Mirror the root.go wiring exactly.
	srv.SetWorkspaceGate(func(r *http.Request) bool {
		return WorkspaceGateDecisionWithDecl(r.RemoteAddr, r.Header.Get("X-Ggcode-Workspace"), r.Header.Get(WorkspaceProofHeader), nil, "/w", srv.DomainKey(), time.Now())
	})
	return srv
}

func issue5667Call(t *testing.T, srv *Server, remoteAddr, apiKey, declared, proof string) (int, string, *JSONRPCResponse) {
	t.Helper()
	body := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"runtime/snapshot\",\"params\":{}}"
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	if declared != "" {
		req.Header.Set("X-Ggcode-Workspace", declared)
	}
	if proof != "" {
		req.Header.Set(WorkspaceProofHeader, proof)
	}
	rec := httptest.NewRecorder()
	srv.a2aMiddleware(srv.handleRPC)(rec, req)
	resp := rec.Result()
	out := JSONRPCResponse{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, rec.Header().Get("X-Auth-Reason"), &out
}

// Acceptance 1 (end-to-end, default-config LAN shape: no API key
// configured = legacy open auth arm): the two public constants no longer
// suffice - the workspace string alone gets -32003 from a non-loopback,
// non-roster caller; adding the key proof passes.
func TestIssue5667ForeignCallerDeniedWithoutProof(t *testing.T) {
	key := issue5667Key(11)
	srv := issue5667Server(t, "", key)
	const foreign = "192.168.31.99:5555"

	code, _, out := issue5667Call(t, srv, foreign, "", "/w", "")
	if code != http.StatusOK || out.Error == nil || out.Error.Code != -32003 {
		t.Fatalf("bare matching declaration must yield -32003, got code=%d resp=%+v", code, out.Error)
	}

	proof, err := BuildWorkspaceProof(key, MethodRuntimeSnapshot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, _, out = issue5667Call(t, srv, foreign, "", "/w", proof)
	if code != http.StatusOK || out.Error != nil {
		t.Fatalf("declaration + valid proof must pass, got code=%d err=%+v", code, out.Error)
	}

	// A proof under a different workspace key does not help.
	foreignProof, _ := BuildWorkspaceProof(issue5667Key(12), MethodRuntimeSnapshot, time.Now())
	code, _, out = issue5667Call(t, srv, foreign, "", "/w", foreignProof)
	if code != http.StatusOK || out.Error == nil || out.Error.Code != -32003 {
		t.Fatalf("foreign-key proof must yield -32003, got code=%d resp=%+v", code, out.Error)
	}
}

// Acceptance 3 (auth-order pin): the declared-workspace headers can never
// bypass the FIRST auth gate - with an API key configured, a request with
// an invalid key gets 401 before the workspace gate runs, even when it
// carries a perfectly valid declaration + proof.
func TestIssue5667DeclaredHeaderCannotBypassAPIKeyAuth(t *testing.T) {
	key := issue5667Key(13)
	srv := issue5667Server(t, "secret-k", key)
	proof, err := BuildWorkspaceProof(key, MethodRuntimeSnapshot, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, apiKey, declared, proof string
		wantStatus                    int
		wantReason                    string
		wantRPCCode                   int
	}{
		{"invalid api key + valid proof", "WRONG", "/w", proof, http.StatusUnauthorized, "api_key_invalid", 0},
		{"missing api key + valid proof", "", "/w", proof, http.StatusUnauthorized, "api_key_missing", 0},
		{"valid api key, no proof, foreign caller", "secret-k", "/w", "", http.StatusOK, "", -32003},
		{"valid api key + valid proof", "secret-k", "/w", proof, http.StatusOK, "", 0},
	}
	for _, tc := range cases {
		code, reason, out := issue5667Call(t, srv, "192.168.31.99:5555", tc.apiKey, tc.declared, tc.proof)
		if code != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d", tc.name, code, tc.wantStatus)
			continue
		}
		if tc.wantStatus == http.StatusUnauthorized && reason != tc.wantReason {
			t.Errorf("%s: X-Auth-Reason = %q, want %q", tc.name, reason, tc.wantReason)
		}
		if tc.wantStatus == http.StatusOK && tc.wantRPCCode != 0 {
			if out.Error == nil || out.Error.Code != tc.wantRPCCode {
				t.Errorf("%s: rpc error = %+v, want code %d", tc.name, out.Error, tc.wantRPCCode)
			}
		}
		if tc.wantStatus == http.StatusOK && tc.wantRPCCode == 0 && out.Error != nil {
			t.Errorf("%s: unexpected rpc error %+v", tc.name, out.Error)
		}
	}
}

// Acceptance 2 support: the caller-side resolver picks the peer
// advertised key from the discovery roster (this is the zero-manual-config
// cross-host path).
func TestIssue5667PeerKeyResolutionFromRoster(t *testing.T) {
	key := issue5667Key(15)
	peer := InstanceInfo{
		ID: "peer", Workspace: "/w", Endpoint: "http://192.168.31.105:64932",
		DomainKeyID: WorkspaceDomainKeyID(key), DomainKey: EncodeDomainKey(key),
	}
	// Endpoint variants normalize to the same peer (scheme-full vs bare).
	for _, ep := range []string{"http://192.168.31.105:64932", "192.168.31.105:64932"} {
		if k := DomainKeyForEndpoint([]InstanceInfo{peer}, ep); k == nil || string(k) != string(key) {
			t.Fatalf("endpoint %q must resolve the peer key", ep)
		}
	}
}
