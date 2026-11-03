package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

func TestServiceNegativeCacheHonorsRetryAfter(t *testing.T) {
	p := &fakeProbe{vendor: "rl-vendor"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.calls.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	svc := NewService()

	svc.Register(&wrapperProbe{p: p, url: srv.URL})

	if _, err := svc.Get(context.Background(), "rl-vendor", "", "k"); err == nil {
		t.Fatal("expected 429 error")
	}

	if _, err := svc.Get(context.Background(), "rl-vendor", "", "k"); err == nil {
		t.Fatal("expected cached 429")
	}
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("rate-limited endpoint re-probed inside Retry-After window: %d calls", n)
	}
}

type wrapperProbe struct {
	p   *fakeProbe
	url string
}

func (w *wrapperProbe) Vendor() string { return w.p.vendor }

func (w *wrapperProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	return nil, getJSON(ctx, w.url, apiKey, &struct{}{})
}

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
