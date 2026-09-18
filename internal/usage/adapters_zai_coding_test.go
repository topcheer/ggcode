package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Coding-plan base (.../api/coding/paas/v4) must normalize to the site
// root before appending the monitor path - the old suffix list missed it
// and produced .../api/coding/paas/v4/api/monitor/... (404), which the
// panel rendered as "unsupported" for zhipu coding-plan users.
func TestZaiProbeCodingPlanBaseNormalization(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"limit_used":25,"limit_total":100}`))
	}))
	defer srv.Close()

	// Derive a coding-plan-shaped base from the test server host.
	base := strings.TrimPrefix(srv.URL, "http://") + "/api/coding/paas/v4"
	info, err := ZaiProbe{}.Fetch(context.Background(), "http://"+base, "test-key")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/api/monitor/usage/quota/limit" {
		t.Fatalf("monitor path must be requested at site root, got %q", gotPath)
	}
	if len(info.Windows) != 1 || info.Windows[0].UsedPercent != 25 {
		t.Fatalf("unexpected windows: %+v", info.Windows)
	}
}
