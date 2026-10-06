package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRPCRetriesTransientFailures(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= 2 {
			// Transient faults: a connection-level error then a 503.
			if attempts.Load() == 1 {
				panic(http.ErrAbortHandler)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Result:  json.RawMessage(`{"id":"t1","status":{"state":"completed"}}`),
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	task, err := c.SendMessage(context.Background(), "test", "hello")
	if err != nil {
		t.Fatalf("SendMessage after transient failures: %v", err)
	}
	if task.ID != "t1" {
		t.Fatalf("task ID = %q, want t1", task.ID)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3 (2 retries)", got)
	}
}

func TestRPCNoRetryOnClientError(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := c.SendMessage(context.Background(), "test", "hello")
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on 4xx)", got)
	}
}

func TestRPCNoRetryOnJSONRPCError(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Error:   &JSONRPCError{Code: -32603, Message: "boom"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := c.SendMessage(context.Background(), "test", "hello")
	if err == nil {
		t.Fatal("expected JSON-RPC error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (application errors are final)", got)
	}
}

func TestIsRetryableHTTPStatus(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 504} {
		if !isRetryableHTTPStatus(code) {
			t.Errorf("isRetryableHTTPStatus(%d) = false, want true", code)
		}
	}
	for _, code := range []int{400, 401, 404, 501} {
		if isRetryableHTTPStatus(code) {
			t.Errorf("isRetryableHTTPStatus(%d) = true, want false", code)
		}
	}
}

func TestRPCRetryRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.SendMessage(ctx, "test", "hello")
	if err == nil {
		t.Fatal("expected error after context deadline")
	}
	// Base backoff is 100ms; jitter keeps total wait under ~300ms for the
	// early attempt, but the cancelled context must cut retries short.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("SendMessage took %v after 50ms deadline; retries ignored ctx", elapsed)
	}
}
