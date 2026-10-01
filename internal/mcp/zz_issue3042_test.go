package mcp

// Regression probe for #3042: the ReadResource MISS path stored the result
// whose Contents slice the returned &result still aliases - the cache-hit
// path clones on read (#3041/09c72cf28) but the store side did not. A caller
// mutating the FIRST (miss) result's Contents polluted every later TTL hit.

import (
	"context"
	"testing"
	"time"
)

func TestIssue3042_MissPathStoreDoesNotAliasCallerSlice(t *testing.T) {
	srv := newCacheableTestServer(func(method string) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "cacheable", "version": "1"},
			}, true
		case "resources/read":
			return map[string]any{
				"contents": []map[string]any{
					{"uri": "file:///a.txt", "text": "original"},
				},
				"ttlMs": 60000,
			}, true
		}
		return nil, false
	})
	defer srv.Close()
	client := newCacheableTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First read: MISS - the response is stored in the cache and the same
	// slice is handed to the caller.
	first, err := client.ReadResource(ctx, "file:///a.txt")
	if err != nil {
		t.Fatalf("first (miss) read: %v", err)
	}
	if len(first.Contents) != 1 || first.Contents[0].Text != "original" {
		t.Fatalf("unexpected first read: %+v", first.Contents)
	}

	// Caller-side mutation of the MISS result - exactly the pollution path:
	// element rewrite (and a sort would do the same) writes through the
	// shared backing array into the stored cache entry.
	first.Contents[0].Text = "polluted"

	// Second read within the TTL: cache HIT must serve the pristine entry.
	second, err := client.ReadResource(ctx, "file:///a.txt")
	if err != nil {
		t.Fatalf("second (hit) read: %v", err)
	}
	if second.Contents[0].Text != "original" {
		t.Fatalf("#3042: cache entry polluted by miss-path caller mutation: got %q, want %q",
			second.Contents[0].Text, "original")
	}
}
