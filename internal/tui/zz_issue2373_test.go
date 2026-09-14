package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #2373: the vendor probe inside SessionUsageSummary must run OFF the
// caller (bubbletea Update) goroutine. Point the active vendor at a server
// that stalls 600ms: the summary itself must return well before the stall
// (the pre-#2373 synchronous svc.Get blocked the whole TUI for the full
// probe). Probe outcome stays silent either way; only latency is pinned.
func TestTUISlashUsageProbeOffUpdateGoroutine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"total_credits":10,"total_usage":2}}`))
	}))
	defer srv.Close()

	m := newTestModel()
	m.activeVendor = "zai"
	m.config = &config.Config{
		Vendor: "zai",
		Vendors: map[string]config.VendorConfig{
			"zai": {Endpoints: map[string]config.EndpointConfig{
				"e": {BaseURL: srv.URL, APIKey: "k"},
			}},
		},
	}
	d := tuiSlashDeps{m: &m}

	start := time.Now()
	out, err := d.SessionUsageSummary()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("usage must never hard-error: %v", err)
	}
	if out == "" {
		t.Fatal("empty output")
	}
	// The synchronous path must not wait for the 600ms probe. 300ms leaves
	// generous headroom for slow CI while still failing the old
	// synchronous form decisively.
	if elapsed > 300*time.Millisecond {
		t.Fatalf("SessionUsageSummary blocked %v on the probe - svc.Get ran on the caller goroutine (Update freeze, #2373)", elapsed)
	}
	// The payload itself now arrives asynchronously via emitIMText; with
	// no emitter wired in the test model the goroutine must simply be
	// silent (no panic). Give it a beat so -race sees the goroutine run.
	time.Sleep(50 * time.Millisecond)
}
