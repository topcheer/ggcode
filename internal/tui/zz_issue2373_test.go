package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

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

	if elapsed > 300*time.Millisecond {
		t.Fatalf("SessionUsageSummary blocked %v on the probe - svc.Get ran on the caller goroutine (Update freeze, #2373)", elapsed)
	}

	time.Sleep(50 * time.Millisecond)
}
