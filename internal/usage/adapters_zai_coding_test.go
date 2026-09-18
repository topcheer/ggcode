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
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"limits":[{"type":"TIME_LIMIT","unit":5,"number":1,"usage":4000,"currentValue":500,"remaining":3500,"percentage":25,"nextResetTime":1789831644997},{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":60,"nextResetTime":1789738694871}]},"success":true}`))
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
	if len(info.Windows) != 2 || info.Windows[0].UsedPercent != 25 || info.Windows[1].UsedPercent != 60 {
		t.Fatalf("unexpected windows: %+v", info.Windows)
	}
}
