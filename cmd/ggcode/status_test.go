package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/runfile"
)

// startMockStatusServer starts a test HTTP server that serves /api/status
// with the given JSON response.
func startMockStatusServer(t *testing.T, statusJSON string) (addr, token string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(statusJSON))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.Listener.Addr().String(), "test-token"
}

func TestFetchStatusSuccess(t *testing.T) {
	statusJSON := `{"pid":12345,"workspace":"/tmp/ws","agent_busy":true,"permission_mode":"auto","vendor":"openai","endpoint":"ep","model":"gpt-4","language":"en","im_adapters":[{"name":"slack","type":"slack","online":true,"muted":false}],"mobile":{"connected":true,"session_id":"sess-123"}}`
	addr, token := startMockStatusServer(t, statusJSON)

	st, err := fetchStatus(addr, token)
	if err != nil {
		t.Fatalf("fetchStatus: %v", err)
	}
	if st.PID != 12345 {
		t.Errorf("PID = %d, want 12345", st.PID)
	}
	if !st.AgentBusy {
		t.Error("AgentBusy should be true")
	}
	if st.PermissionMode != "auto" {
		t.Errorf("Mode = %q, want auto", st.PermissionMode)
	}
	if len(st.IMAdapters) != 1 || st.IMAdapters[0].Name != "slack" {
		t.Errorf("unexpected IM adapters: %+v", st.IMAdapters)
	}
	if !st.MobileConn.Connected {
		t.Error("Mobile should be connected")
	}
}

func TestFetchStatusWrongToken(t *testing.T) {
	addr, _ := startMockStatusServer(t, `{}`)
	_, err := fetchStatus(addr, "wrong-token")
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
}

func TestFetchStatusUnreachable(t *testing.T) {
	_, err := fetchStatus("127.0.0.1:1", "token")
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
}

func writeTestPortFile(t *testing.T, sessionID, workspace, addr, token string) {
	t.Helper()
	if err := runfile.Write(runfile.PortFile{
		Addr:      addr,
		Token:     token,
		PID:       os.Getpid(),
		SessionID: sessionID,
		Workspace: workspace,
		Mode:      "auto",
	}); err != nil {
		t.Fatalf("Write port file: %v", err)
	}
	t.Cleanup(func() { runfile.Remove(sessionID) })
}

func TestRunStatusListEmpty(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var buf strings.Builder
	err := runStatusList(&buf, false, false, false, false)
	if err != nil {
		t.Fatalf("runStatusList: %v", err)
	}
	if !strings.Contains(buf.String(), "No running") {
		t.Errorf("expected 'No running', got %q", buf.String())
	}
}

func TestRunStatusListEmptyJSON(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var buf strings.Builder
	err := runStatusList(&buf, false, false, false, true)
	if err != nil {
		t.Fatalf("runStatusList: %v", err)
	}
	if buf.String() != "[]\n" {
		t.Errorf("expected '[]\\n', got %q", buf.String())
	}
}

func TestRunStatusListWithInstance(t *testing.T) {
	statusJSON := `{"pid":99999,"workspace":"/test/ws","agent_busy":true,"permission_mode":"bypass","vendor":"anthropic","endpoint":"ep1","model":"claude-4","language":"zh","im_adapters":[{"name":"slack","type":"slack","online":true,"channel":"#general"}],"mobile":{"connected":false}}`
	addr, token := startMockStatusServer(t, statusJSON)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	writeTestPortFile(t, "sess-list-1", "/test/ws", addr, token)

	var buf strings.Builder
	err := runStatusList(&buf, false, false, false, false)
	if err != nil {
		t.Fatalf("runStatusList: %v", err)
	}
	out := buf.String()
	pidStr := fmt.Sprintf("%d", os.Getpid())
	if !strings.Contains(out, pidStr) {
		t.Errorf("expected PID %s in output, got: %s", pidStr, out)
	}
	if !strings.Contains(out, "claude-4") {
		t.Errorf("expected model in output, got: %s", out)
	}
}

func TestRunStatusListMultipleSameWorkspace(t *testing.T) {
	statusJSON1 := `{"pid":1,"workspace":"/shared/ws","agent_busy":true,"permission_mode":"auto","vendor":"v","endpoint":"e","model":"m1","language":"en","im_adapters":[],"mobile":{"connected":false}}`
	statusJSON2 := `{"pid":2,"workspace":"/shared/ws","agent_busy":false,"permission_mode":"bypass","vendor":"v","endpoint":"e","model":"m2","language":"en","im_adapters":[],"mobile":{"connected":false}}`
	addr1, token1 := startMockStatusServer(t, statusJSON1)
	addr2, token2 := startMockStatusServer(t, statusJSON2)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	writeTestPortFile(t, "sess-multi-1", "/shared/ws", addr1, token1)
	writeTestPortFile(t, "sess-multi-2", "/shared/ws", addr2, token2)

	var buf strings.Builder
	runStatusList(&buf, false, false, false, false)
	out := buf.String()

	// Both models should appear
	if !strings.Contains(out, "m1") {
		t.Errorf("expected model m1 in output: %s", out)
	}
	if !strings.Contains(out, "m2") {
		t.Errorf("expected model m2 in output: %s", out)
	}
	// Both session IDs should appear
	if !strings.Contains(out, "sess-mul") {
		t.Errorf("expected session IDs in output: %s", out)
	}
}

func TestRunStatusListAgentFilter(t *testing.T) {
	statusJSON := `{"agent_busy":true,"permission_mode":"auto","model":"gpt-4"}`
	addr, token := startMockStatusServer(t, statusJSON)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeTestPortFile(t, "sess-agent-1", "/test/ws2", addr, token)

	var buf strings.Builder
	runStatusList(&buf, true, false, false, false)
	out := buf.String()
	if !strings.Contains(out, "busy") {
		t.Errorf("expected 'busy' in agent filter output: %s", out)
	}
}

func TestRunStatusListIMFilter(t *testing.T) {
	statusJSON := `{"im_adapters":[{"name":"telegram","type":"telegram","online":true,"channel":"@bot"},{"name":"slack","type":"slack","online":false}]}`
	addr, token := startMockStatusServer(t, statusJSON)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeTestPortFile(t, "sess-im-1", "/test/ws3", addr, token)

	var buf strings.Builder
	runStatusList(&buf, false, true, false, false)
	out := buf.String()
	if !strings.Contains(out, "telegram") {
		t.Errorf("expected 'telegram' in IM filter output: %s", out)
	}
	if !strings.Contains(out, "slack") {
		t.Errorf("expected 'slack' in IM filter output: %s", out)
	}
}

func TestRunStatusListMobileFilter(t *testing.T) {
	statusJSON := `{"mobile":{"connected":true,"session_id":"sess-abc"}}`
	addr, token := startMockStatusServer(t, statusJSON)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeTestPortFile(t, "sess-mob-1", "/test/ws4", addr, token)

	var buf strings.Builder
	runStatusList(&buf, false, false, true, false)
	out := buf.String()
	if !strings.Contains(out, "yes") {
		t.Errorf("expected 'yes' for connected mobile: %s", out)
	}
	if !strings.Contains(out, "sess-abc") {
		t.Errorf("expected session ID in output: %s", out)
	}
}

func TestRunStatusListJSON(t *testing.T) {
	statusJSON := `{"pid":99999,"workspace":"/test/ws5","agent_busy":false,"permission_mode":"auto","vendor":"x","endpoint":"y","model":"z","language":"en","im_adapters":[],"mobile":{"connected":false}}`
	addr, token := startMockStatusServer(t, statusJSON)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeTestPortFile(t, "sess-json-1", "/test/ws5", addr, token)

	var buf strings.Builder
	runStatusList(&buf, false, false, false, true)

	var results []instanceStatus
	if err := json.Unmarshal([]byte(buf.String()), &results); err != nil {
		t.Fatalf("unmarshal JSON output: %v\noutput: %s", err, buf.String())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status == nil {
		t.Fatal("expected non-nil status")
	}
	if results[0].Status.Model != "z" {
		t.Errorf("Model = %q, want z", results[0].Status.Model)
	}
	if results[0].Port.SessionID != "sess-json-1" {
		t.Errorf("SessionID = %q, want sess-json-1", results[0].Port.SessionID)
	}
}

func TestRunStatusGetMultipleSessions(t *testing.T) {
	statusJSON1 := `{"pid":1,"workspace":"/shared/get","agent_busy":true,"permission_mode":"bypass","vendor":"openai","endpoint":"ep","model":"gpt-4o","language":"en","im_adapters":[],"mobile":{"connected":false}}`
	statusJSON2 := `{"pid":2,"workspace":"/shared/get","agent_busy":false,"permission_mode":"auto","vendor":"anthropic","endpoint":"ep2","model":"claude","language":"en","im_adapters":[],"mobile":{"connected":false}}`
	addr1, token1 := startMockStatusServer(t, statusJSON1)
	addr2, token2 := startMockStatusServer(t, statusJSON2)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeTestPortFile(t, "sess-get-1", "/shared/get", addr1, token1)
	writeTestPortFile(t, "sess-get-2", "/shared/get", addr2, token2)

	var buf strings.Builder
	err := runStatusGet(&buf, "/shared/get", false)
	if err != nil {
		t.Fatalf("runStatusGet: %v", err)
	}
	out := buf.String()
	// Should have both sessions
	if !strings.Contains(out, "gpt-4o") {
		t.Errorf("expected gpt-4o in output:\n%s", out)
	}
	if !strings.Contains(out, "claude") {
		t.Errorf("expected claude in output:\n%s", out)
	}
	if !strings.Contains(out, "---") {
		t.Errorf("expected separator between sessions:\n%s", out)
	}
}

func TestRunStatusGetNotFound(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var buf strings.Builder
	err := runStatusGet(&buf, "/nonexistent/workspace", false)
	if err == nil {
		t.Fatal("expected error for non-existent workspace")
	}
}

func TestRunStatusGetJSON(t *testing.T) {
	statusJSON := `{"pid":12321,"workspace":"/test/ws7","agent_busy":false,"permission_mode":"auto","vendor":"","endpoint":"","model":"","language":"","im_adapters":[],"mobile":{"connected":false}}`
	addr, token := startMockStatusServer(t, statusJSON)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeTestPortFile(t, "sess-getjson-1", "/test/ws7", addr, token)

	var buf strings.Builder
	err := runStatusGet(&buf, "/test/ws7", true)
	if err != nil {
		t.Fatalf("runStatusGet JSON: %v", err)
	}

	var results []struct {
		PortFile runfile.PortFile   `json:"port_file"`
		Status   *runtimeStatusJSON `json:"status"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &results); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, buf.String())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].PortFile.SessionID != "sess-getjson-1" {
		t.Errorf("SessionID = %q", results[0].PortFile.SessionID)
	}
}

// --- Helper function tests ---

func TestBusyIcon(t *testing.T) {
	if busyIcon(true) != "busy" {
		t.Error("busyIcon(true) should be 'busy'")
	}
	if busyIcon(false) != "idle" {
		t.Error("busyIcon(false) should be 'idle'")
	}
}

func TestOnlineIcon(t *testing.T) {
	if onlineIcon(true) != "online" {
		t.Error("onlineIcon(true) should be 'online'")
	}
	if onlineIcon(false) != "offline" {
		t.Error("onlineIcon(false) should be 'offline'")
	}
}

func TestConnIcon(t *testing.T) {
	if connIcon(true) != "connected" {
		t.Error("connIcon(true) should be 'connected'")
	}
	if connIcon(false) != "—" {
		t.Error("connIcon(false) should be '—'")
	}
}

func TestAnyIMOnline(t *testing.T) {
	if anyIMOnline(nil) {
		t.Error("nil should be false")
	}
	if anyIMOnline([]imAdapterJSON{{Online: false}}) {
		t.Error("all offline should be false")
	}
	if !anyIMOnline([]imAdapterJSON{{Online: false}, {Online: true}}) {
		t.Error("any online should be true")
	}
}

func TestShortPath(t *testing.T) {
	home := os.Getenv("HOME")
	result := shortPath(home + "/projects/test")
	if result != "~/projects/test" {
		t.Errorf("shortPath = %q, want ~/projects/test", result)
	}
	result = shortPath("/opt/other")
	if result != "/opt/other" {
		t.Errorf("shortPath = %q, want /opt/other", result)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("abc", 10) != "abc" {
		t.Error("should not truncate short strings")
	}
	result := truncate("abcdefghij", 5)
	if result != "abcde…" {
		t.Errorf("truncate = %q, want abcde…", result)
	}
}

func TestDefaultStr(t *testing.T) {
	if defaultStr("", "fallback") != "fallback" {
		t.Error("empty should return fallback")
	}
	if defaultStr("value", "fallback") != "value" {
		t.Error("non-empty should return value")
	}
}

func TestBusyLabel(t *testing.T) {
	if busyLabel(true) != "busy (working)" {
		t.Error("wrong label for busy")
	}
	if busyLabel(false) != "idle" {
		t.Error("wrong label for idle")
	}
}
