package usage

// #2150 batch 1: probe layer. Three vendor shapes pinned against a mock
// server (zai quota window, deepseek multi-currency balance picking USD,
// moonshot nested balance), plus the service cache contract (TTL reuse,
// negative cache, singleflight merge).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestZaiProbeQuotaWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/monitor/usage/quota/limit" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":200,"msg":"ok","data":{"limits":[{"type":"TIME_LIMIT","unit":5,"number":1,"usage":4000,"currentValue":500,"remaining":3500,"percentage":25,"nextResetTime":1789831644997},{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":60,"nextResetTime":1789738694871}]},"success":true}`))
	}))
	defer srv.Close()
	info, err := ZaiProbe{}.Fetch(context.Background(), srv.URL+"/api/paas/v4", "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 1 || info.Windows[0].Label != "5h" || info.Windows[0].UsedPercent != 60 {
		t.Fatalf("only the TOKENS_LIMIT(unit=3) entry may show as 5h; TIME_LIMIT is a different meter: %+v", info.Windows)
	}
}

func TestDeepSeekBalancePrefersUSD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":88.5},{"currency":"USD","total_balance":12.34}]}`))
	}))
	defer srv.Close()
	info, err := DeepSeekProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Balance == nil || *info.Balance != 12.34 {
		t.Fatalf("balance = %v", info.Balance)
	}
}

func TestMoonshotNestedBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"available_balance":42.0}}`))
	}))
	defer srv.Close()
	info, err := MoonshotProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Balance == nil || *info.Balance != 42.0 {
		t.Fatalf("balance = %v", info.Balance)
	}
}

func TestServiceCacheAndSingleflight(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`{"data":{"available_balance":1}}`))
	}))
	defer srv.Close()
	s := NewService()
	s.Register(moonshotAt(srv.URL))
	ctx := context.Background()
	// singleflight: 5 concurrent gets -> exactly 1 hit
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.Get(ctx, "moonshot", "", "k") }()
	}
	wg.Wait()
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("singleflight violated: %d hits", hits)
	}
	// TTL cache: sequential get -> still 1
	if _, err := s.Get(ctx, "moonshot", "", "k"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("TTL cache violated: %d hits", hits)
	}
	// invalidate -> fresh
	s.Invalidate("moonshot")
	_, _ = s.Get(ctx, "moonshot", "", "k")
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("invalidate violated: %d hits", hits)
	}
}

func TestServiceUnsupportedVendor(t *testing.T) {
	s := NewService()
	if _, err := s.Get(context.Background(), "nope", "", ""); err != ErrUnsupported {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

// moonshotAt rewrites the probe target: the service passes the ACTIVE
// vendor base URL through, so point it at the mock server.
type moonshotAtP struct{ url string }

func (p moonshotAtP) Vendor() string { return "moonshot" }
func (p moonshotAtP) Fetch(ctx context.Context, _, apiKey string) (*UsageInfo, error) {
	return MoonshotProbe{}.Fetch(ctx, p.url, apiKey)
}
func moonshotAt(url string) Probe { return moonshotAtP{url: url} }

var _ = time.Second
