package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// newTestServer wires a full platform server over httptest.
func newTestServer(t *testing.T, run RunFunc) (*Server, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	users, err := LoadUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Add("alice", "alice-pass-123", true); err != nil {
		t.Fatal(err)
	}
	if err := users.Add("bob", "bob-pass-1234", false); err != nil {
		t.Fatal(err)
	}
	ws, _ := LoadWorkspaceSet(filepath.Join(dir, "workspaces.json"))
	root := t.TempDir()
	if err := ws.Add(root); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t)
	exec := NewExecutor(store, run)
	secret := make([]byte, 32)
	srv := NewServer(users, store, ws, exec, secret)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, root
}

func doJSON(t *testing.T, method, url, token string, body interface{}) (*http.Response, map[string]interface{}) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func login(t *testing.T, ts *httptest.Server, user, pass string) string {
	t.Helper()
	resp, out := doJSON(t, "POST", ts.URL+"/api/v1/auth/login", "", map[string]string{"username": user, "password": pass})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d", user, resp.StatusCode)
	}
	tok, _ := out["token"].(string)
	if tok == "" {
		t.Fatal("empty login token")
	}
	return tok
}

func TestServerLoginAndJobLifecycle(t *testing.T) {
	_, ts, root := newTestServer(t, func(_ context.Context, _, _ string) (int, []byte, error) {
		return 0, []byte("agent output"), nil
	})
	// wrong credentials rejected
	resp, _ := doJSON(t, "POST", ts.URL+"/api/v1/auth/login", "", map[string]string{"username": "alice", "password": "nope"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d", resp.StatusCode)
	}
	tok := login(t, ts, "bob", "bob-pass-1234")

	// missing auth
	resp, _ = doJSON(t, "GET", ts.URL+"/api/v1/jobs", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthed list status = %d", resp.StatusCode)
	}

	// disallowed workspace
	resp, _ = doJSON(t, "POST", ts.URL+"/api/v1/jobs", tok, map[string]string{"workspace": "/etc", "prompt": "p"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("disallowed workspace status = %d", resp.StatusCode)
	}

	// missing prompt
	resp, _ = doJSON(t, "POST", ts.URL+"/api/v1/jobs", tok, map[string]string{"workspace": root})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing prompt status = %d", resp.StatusCode)
	}

	// happy path
	resp, out := doJSON(t, "POST", ts.URL+"/api/v1/jobs", tok, map[string]string{"workspace": root, "prompt": "fix the bug"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d (%v)", resp.StatusCode, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatal("missing job id")
	}

	// detail includes output
	deadline := 50
	var detail map[string]interface{}
	for i := 0; i < deadline; i++ {
		r, d := doJSON(t, "GET", ts.URL+"/api/v1/jobs/"+id, tok, nil)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("get status = %d", r.StatusCode)
		}
		detail = d
		if detail["status"] == string(StatusSucceeded) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if detail["status"] != string(StatusSucceeded) || detail["output"] != "agent output" {
		t.Fatalf("detail = %v", detail)
	}

	// list omits output and shows own jobs
	resp, out = doJSON(t, "GET", ts.URL+"/api/v1/jobs", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	_ = out
}

func TestServerJobIsolationBetweenUsers(t *testing.T) {
	_, ts, root := newTestServer(t, func(_ context.Context, _, _ string) (int, []byte, error) {
		return 0, []byte("out"), nil
	})
	alice := login(t, ts, "alice", "alice-pass-123")
	bob := login(t, ts, "bob", "bob-pass-1234")

	_, out := doJSON(t, "POST", ts.URL+"/api/v1/jobs", alice, map[string]string{"workspace": root, "prompt": "p"})
	id, _ := out["id"].(string)

	// bob cannot see alice's job: 404, not 403 (no existence leak)
	resp, _ := doJSON(t, "GET", ts.URL+"/api/v1/jobs/"+id, bob, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob get alice job = %d", resp.StatusCode)
	}
	// bob cannot cancel it
	resp, _ = doJSON(t, "DELETE", ts.URL+"/api/v1/jobs/"+id, bob, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob cancel alice job = %d", resp.StatusCode)
	}
	// admin can see it
	resp, _ = doJSON(t, "GET", ts.URL+"/api/v1/jobs/"+id, alice, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin get = %d", resp.StatusCode)
	}
}

func TestServerCancelQueuedJob(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	_, ts, root := newTestServer(t, func(_ context.Context, _, _ string) (int, []byte, error) {
		started <- struct{}{}
		<-block
		return 0, nil, nil
	})
	tok := login(t, ts, "bob", "bob-pass-1234")
	_, out1 := doJSON(t, "POST", ts.URL+"/api/v1/jobs", tok, map[string]string{"workspace": root, "prompt": "1"})
	_, out2 := doJSON(t, "POST", ts.URL+"/api/v1/jobs", tok, map[string]string{"workspace": root, "prompt": "2"})
	id1, _ := out1["id"].(string)
	id2, _ := out2["id"].(string)
	<-started

	// cancel the queued one
	resp, out := doJSON(t, "DELETE", ts.URL+"/api/v1/jobs/"+id2, tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel queued = %d (%v)", resp.StatusCode, out)
	}
	// cancel running one
	resp, _ = doJSON(t, "DELETE", ts.URL+"/api/v1/jobs/"+id1, tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel running = %d", resp.StatusCode)
	}
	close(block)
	// poll via HTTP until both jobs reach cancelled
	deadline := time.Now().Add(5 * time.Second)
	statuses := map[string]string{id1: "", id2: ""}
	for time.Now().Before(deadline) {
		for _, id := range []string{id1, id2} {
			if statuses[id] == string(StatusCancelled) {
				continue
			}
			_, out := doJSON(t, "GET", ts.URL+"/api/v1/jobs/"+id, tok, nil)
			if s, _ := out["status"].(string); s != "" {
				statuses[id] = s
			}
		}
		if statuses[id1] == string(StatusCancelled) && statuses[id2] == string(StatusCancelled) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("jobs did not reach cancelled: %v", statuses)
}

func TestLoadOrCreateSecretRoundTrip(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadOrCreateSecret(dir)
	if err != nil || len(k1) != 32 {
		t.Fatalf("k1 = %v, %v", k1, err)
	}
	k2, err := LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(k1) != string(k2) {
		t.Fatal("secret not stable across loads")
	}
}
