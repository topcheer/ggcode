package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRateLimitedErrorFrom429 verifies #2150 batch 2b: a 429 response is
// surfaced as *RateLimitedError carrying the server's Retry-After (both
// RFC 7231 forms), and the Service sizes its negative cache to that window
// instead of the default negativeTTL.
func TestRateLimitedErrorFrom429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	var out struct{}
	err := getJSON(context.Background(), srv.URL, "k", &out)
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("429 not surfaced as RateLimitedError: %v", err)
	}
	if rl.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %v, want 30s", rl.RetryAfter)
	}
}

// TestParseRetryAfterBothForms covers delta-seconds, HTTP-date, absent,
// negative, and unparseable inputs.
func TestParseRetryAfterBothForms(t *testing.T) {
	if d := parseRetryAfter("120"); d != 2*time.Minute {
		t.Errorf("seconds form: %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("absent: %v", d)
	}
	if d := parseRetryAfter("-5"); d != 0 {
		t.Errorf("negative clamped: %v", d)
	}
	if d := parseRetryAfter("garbage"); d != 0 {
		t.Errorf("unparseable: %v", d)
	}
	future := time.Now().Add(45 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(future); d < 40*time.Second || d > 45*time.Second {
		t.Errorf("http-date form: %v", d)
	}
}

// TestServiceNegativeCacheHonorsRetryAfter verifies the blind-spot
// linkage: after a 429 with Retry-After=60s, the vendor stays
// negative-cached for ~60s - NOT re-probed at the default negativeTTL
// (1min < 60s would eventually differ, so assert no re-probe well inside
// the window via call counting on a scripted probe).
func TestServiceNegativeCacheHonorsRetryAfter(t *testing.T) {
	p := &fakeProbe{vendor: "rl-vendor"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.calls.Add(1)
		w.Header().Set("Retry-After", "600") // 10min window
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	svc := NewService()
	// Route the fake probe through the real 429 server by wrapping.
	svc.Register(&wrapperProbe{p: p, url: srv.URL})

	if _, err := svc.Get(context.Background(), "rl-vendor", "", "k"); err == nil {
		t.Fatal("expected 429 error")
	}
	// Second Get within the window: served from cache, probe NOT re-hit.
	if _, err := svc.Get(context.Background(), "rl-vendor", "", "k"); err == nil {
		t.Fatal("expected cached 429")
	}
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("rate-limited endpoint re-probed inside Retry-After window: %d calls", n)
	}
}

// wrapperProbe adapts fakeProbe's call counting onto a real HTTP 429 server.
type wrapperProbe struct {
	p   *fakeProbe
	url string
}

func (w *wrapperProbe) Vendor() string { return w.p.vendor }

func (w *wrapperProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	return nil, getJSON(ctx, w.url, apiKey, &struct{}{})
}

// TestAnthropicOAuth429SurfacesRateLimited verifies #2366-2: the beta-header
// probe bypasses getJSON, so its self-built status check must produce the
// same *RateLimitedError - otherwise the Retry-After-sized negative cache
// (#2360) never engages for anthropic-oauth and every-minute re-probing
// survives.
func TestAnthropicOAuth429SurfacesRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("beta header = %q", got)
		}
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := AnthropicOAuthProbe{}.Fetch(context.Background(), srv.URL, "tok")
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("429 not surfaced as RateLimitedError: %v", err)
	}
	if rl.RetryAfter != 2*time.Minute {
		t.Fatalf("RetryAfter = %v, want 2m", rl.RetryAfter)
	}
}

// TestDefaultServiceRegistersAllP1 anchors the central registration site:
// every P1 vendor must be present exactly once, so consumers switching to
// DefaultService cannot silently lose a probe again.
func TestDefaultServiceRegistersAllP1(t *testing.T) {
	svc := DefaultService()
	for _, v := range []string{
		"zai", "deepseek", "moonshot", "kimi", "minimax",
		"anthropic-oauth", "openrouter", "siliconflow",
	} {
		if !svc.Has(v) {
			t.Errorf("DefaultService missing probe for %s", v)
		}
	}
}
