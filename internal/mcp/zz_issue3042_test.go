package mcp

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

	first, err := client.ReadResource(ctx, "file:///a.txt")
	if err != nil {
		t.Fatalf("first (miss) read: %v", err)
	}
	if len(first.Contents) != 1 || first.Contents[0].Text != "original" {
		t.Fatalf("unexpected first read: %+v", first.Contents)
	}

	first.Contents[0].Text = "polluted"

	second, err := client.ReadResource(ctx, "file:///a.txt")
	if err != nil {
		t.Fatalf("second (hit) read: %v", err)
	}
	if second.Contents[0].Text != "original" {
		t.Fatalf("#3042: cache entry polluted by miss-path caller mutation: got %q, want %q",
			second.Contents[0].Text, "original")
	}
}
