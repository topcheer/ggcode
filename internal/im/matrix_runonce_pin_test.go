package im

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// matrixRunOncePinServer builds a hermetic fake homeserver for runOnce pin
// tests. whoamiJSON "" means "reject whoami with 401 M_UNKNOWN_TOKEN";
// syncFailCode, when non-zero, is returned for the filter endpoint (the
// deterministic fatal path inside SyncWithContext: CreateFilter errors are
// returned directly, unlike /sync errors which OnFailedSync retries).
type matrixRunOncePinServer struct {
	srv        *httptest.Server
	syncHits   atomic.Int32
	whoamiHits atomic.Int32
	whoamiOK   bool
	failFilter bool
}

func (m *matrixRunOncePinServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/account/whoami"):
			m.whoamiHits.Add(1)
			if !m.whoamiOK {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN","error":"Invalid token"}`))
				return
			}
			_, _ = w.Write([]byte(`{"user_id":"@pin:srv","device_id":"DEV"}`))
		case m.failFilter && strings.HasSuffix(r.URL.Path, "/filter"):
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN","error":"Invalid token"}`))
		case strings.HasSuffix(r.URL.Path, "/sync"):
			// Fatal 401: OnFailedSync would retry /sync forever, so pin
			// tests must never reach this path (see filter path above).
			m.syncHits.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN","error":"Invalid token"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
}

func newMatrixRunOncePinServer(t *testing.T, whoamiOK, failFilter bool) *matrixRunOncePinServer {
	t.Helper()
	m := &matrixRunOncePinServer{whoamiOK: whoamiOK, failFilter: failFilter}
	m.srv = httptest.NewServer(m.handler())
	t.Cleanup(m.srv.Close)
	return m
}

// newMatrixRunOnceAdapter constructs a minimal matrixAdapter for runOnce
// pin tests: real Manager (publishState dereferences manager without a nil
// guard), isolated HOME (config.ConfigDir panics on the real home from
// tests via guardRealHomeDir).
func newMatrixRunOnceAdapter(t *testing.T, homeserver string, closed bool) *matrixAdapter {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return &matrixAdapter{
		name:       "runonce-pin",
		homeserver: homeserver,
		token:      "pin-token",
		manager:    NewManager(),
		closed:     closed,
	}
}

// TestRunOncePin_ClientInitError pins gate 1: an unparseable homeserver URL
// must surface as "client init: ..." before any network I/O.
func TestRunOncePin_ClientInitError(t *testing.T) {
	a := newMatrixRunOnceAdapter(t, "://bad homeserver url", false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := a.runOnce(ctx)
	if err == nil {
		t.Fatal("expected client init error for unparseable homeserver")
	}
	if !strings.HasPrefix(err.Error(), "client init: ") {
		t.Fatalf("error must be wrapped as \"client init: ...\", got %q", err.Error())
	}
}

// TestRunOncePin_WhoamiError pins gate 2: client init passing but whoami
// failing (401 M_UNKNOWN_TOKEN) must surface as "whoami: ..." before any
// state is published.
func TestRunOncePin_WhoamiError(t *testing.T) {
	srv := newMatrixRunOncePinServer(t, false, false)
	a := newMatrixRunOnceAdapter(t, srv.srv.URL, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := a.runOnce(ctx)
	if err == nil {
		t.Fatal("expected whoami error for 401 whoami response")
	}
	if !strings.HasPrefix(err.Error(), "whoami: ") {
		t.Fatalf("error must be wrapped as \"whoami: ...\", got %q", err.Error())
	}
	if srv.whoamiHits.Load() == 0 {
		t.Fatal("whoami endpoint was never called")
	}
}

// TestRunOncePin_ClosedBeforeSync pins the #2127 gate: Close() racing the
// multi-RTT setup must produce a nil error early-return with SyncWithContext
// never reached (no ghost connection), while whoami still happens first
// (gate order: whoami -> ... -> closedNow recheck under cancelFn lock).
func TestRunOncePin_ClosedBeforeSync(t *testing.T) {
	srv := newMatrixRunOncePinServer(t, true, false)
	a := newMatrixRunOnceAdapter(t, srv.srv.URL, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := a.runOnce(ctx)
	if err != nil {
		t.Fatalf("closedNow path must return nil, got %v", err)
	}
	if srv.whoamiHits.Load() == 0 {
		t.Fatal("whoami must run before the closedNow recheck (gate order)")
	}
	if srv.syncHits.Load() != 0 {
		t.Fatal("SyncWithContext must not be reached when closed mid-setup")
	}
	a.mu.RLock()
	gotCancel := a.cancelFn != nil
	a.mu.RUnlock()
	if !gotCancel {
		t.Fatal("cancelFn must be registered even on the closedNow path")
	}
}

// TestRunOncePin_SyncPhaseErrorWrapped pins gate 6: an error escaping
// SyncWithContext (deterministic fatal CreateFilter 401 inside the sync
// loop) must be wrapped as "sync: ..." and returned to the caller.
func TestRunOncePin_SyncPhaseErrorWrapped(t *testing.T) {
	srv := newMatrixRunOncePinServer(t, true, true)
	a := newMatrixRunOnceAdapter(t, srv.srv.URL, false)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := a.runOnce(ctx)
	if err == nil {
		t.Fatal("expected sync phase error to propagate")
	}
	if !strings.HasPrefix(err.Error(), "sync: ") {
		t.Fatalf("error must be wrapped as \"sync: ...\", got %q", err.Error())
	}
	if srv.whoamiHits.Load() == 0 {
		t.Fatal("whoami endpoint was never called")
	}
}
