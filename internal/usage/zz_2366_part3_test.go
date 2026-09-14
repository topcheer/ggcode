package usage

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// #2366 part 3: a 429 on the credits endpoint must surface RateLimitedError
// instead of falling through to the limit endpoint (double request against a
// throttled host, and the Retry-After hint would be lost).
func TestOpenrouterCredits429DoesNotDegrade(t *testing.T) {
	var creditsCalled, limitCalled int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/credits", func(w http.ResponseWriter, r *http.Request) {
		creditsCalled++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	mux.HandleFunc("/api/v1/limit", func(w http.ResponseWriter, r *http.Request) {
		limitCalled++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"usage_limit":100,"usage":10}}`))
	})
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve(ln)

	probe := OpenrouterProbe{}
	_, err = probe.Fetch(context.Background(), "http://"+ln.Addr().String(), "k")
	if creditsCalled != 1 {
		t.Fatalf("credits called %d times, want 1", creditsCalled)
	}
	if limitCalled != 0 {
		t.Fatalf("limit endpoint must not be hit after a 429 on credits, hit %d times", limitCalled)
	}
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("want RateLimitedError, got %T %v", err, err)
	}
	if rl.RetryAfter != 120*time.Second {
		t.Fatalf("RetryAfter = %v, want 120s", rl.RetryAfter)
	}
}

// Non-429 credits failures still degrade to the limit endpoint (pre-existing
// behavior preserved by part 3).
func TestOpenrouterNon429StillDegrades(t *testing.T) {
	var limitCalled int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/credits", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // bad key on credits only
	})
	mux.HandleFunc("/api/v1/limit", func(w http.ResponseWriter, r *http.Request) {
		limitCalled++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"usage_limit":100,"usage":10}}`))
	})
	srv := &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve(ln)

	probe := OpenrouterProbe{}
	info, err := probe.Fetch(context.Background(), "http://"+ln.Addr().String(), "k")
	if err != nil {
		t.Fatalf("degrade to limit should succeed, got %v", err)
	}
	if limitCalled != 1 {
		t.Fatalf("limit called %d times, want 1", limitCalled)
	}
	if info == nil || info.Source != "key-limit" || len(info.Windows) != 1 {
		t.Fatalf("want key-limit info with one window, got %+v", info)
	}
}
