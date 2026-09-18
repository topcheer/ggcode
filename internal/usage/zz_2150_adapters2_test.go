package usage

// #2150 batch 1 (rest): the five remaining P1 probes pinned against
// mock servers, including the openrouter credits->key-limit degrade.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKimiProbeWindows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Constant endpoint path regardless of the configured base shape.
		if r.URL.Path != "/coding/v1/usages" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"usage":{"limit":"100","used":"7","remaining":"93","resetTime":"2026-09-22T12:53:21Z"},"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","used":"1","remaining":"99","resetTime":"2026-09-18T13:53:21Z"}}],"usages":{"limit_5h":{"used_ratio":0.005,"reset_time":"2026-09-18T13:53:19Z"},"limit_7d":{"used_ratio":0.067,"reset_time":"2026-09-22T12:53:20Z"}}}`))
	}))
	defer srv.Close()
	info, err := KimiProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 2 {
		t.Fatalf("windows = %+v", info.Windows)
	}
	if info.Windows[0].Label != "5h" || info.Windows[0].UsedPercent != 0.5 {
		t.Fatalf("5h window = %+v", info.Windows[0])
	}
	if info.Windows[1].Label != "7d" || info.Windows[1].UsedPercent != 6.7 {
		t.Fatalf("7d window = %+v", info.Windows[1])
	}
}

func TestMinimaxProbeRemainToUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model_remains":[{"general":{"five_hour_remain_percent":25,"weekly_remain_percent":80}}]}`))
	}))
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 2 || info.Windows[0].UsedPercent != 75 || info.Windows[1].UsedPercent != 20 {
		t.Fatalf("remain->used conversion wrong: %+v", info.Windows)
	}
}

func TestAnthropicOAuthBetaHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Errorf("missing beta header")
		}
		w.Write([]byte(`{"five_hour":{"utilization":0.96,"resets_at":"2026-09-15T08:00:00Z"},"seven_day":{"utilization":0.41}}`))
	}))
	defer srv.Close()
	info, err := AnthropicOAuthProbe{}.Fetch(context.Background(), srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 2 || info.Windows[0].UsedPercent != 96 || info.Windows[1].UsedPercent != 41 {
		t.Fatalf("utilization->percent: %+v", info.Windows)
	}
}

func TestOpenrouterCreditsPrimary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/credits" {
			w.Write([]byte(`{"data":{"total_credits":50,"total_usage":12.5}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	}))
	defer srv.Close()
	info, err := OpenrouterProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Balance == nil || *info.Balance != 37.5 {
		t.Fatalf("balance = %v", info.Balance)
	}
}

func TestOpenrouterDegradesToKeyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/credits" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/api/v1/limit" {
			w.Write([]byte(`{"data":{"usage_limit":10,"usage":7}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	}))
	defer srv.Close()
	info, err := OpenrouterProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Balance != nil || len(info.Windows) != 1 || info.Windows[0].UsedPercent != 70 {
		t.Fatalf("degraded shape wrong: %+v", info)
	}
}

func TestSiliconflowBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"balance":66.6}}`))
	}))
	defer srv.Close()
	info, err := SiliconflowProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Balance == nil || *info.Balance != 66.6 {
		t.Fatalf("balance = %v", info.Balance)
	}
}
