package mcp

import (
	"context"
	"testing"
)

func TestReadResourceCacheHitClonesContents(t *testing.T) {
	client := &Client{name: "clonecheck"}
	original := ReadResourceResult{
		Contents: []ResourceContent{{URI: "file:///a.txt", Text: "original"}},
	}
	client.storeListingsCache(cacheResourceRead, "file:///a.txt", original, []CacheableResult{{TTLms: 60000}}, client.listingCache.epoch(cacheResourceRead))

	first, err := client.ReadResource(context.Background(), "file:///a.txt")
	if err != nil {
		t.Fatalf("cache-hit read must not error: %v", err)
	}
	if len(first.Contents) != 1 || first.Contents[0].Text != "original" {
		t.Fatalf("unexpected cached content: %+v", first.Contents)
	}

	first.Contents[0].Text = "polluted"

	second, err := client.ReadResource(context.Background(), "file:///a.txt")
	if err != nil {
		t.Fatalf("second cache-hit read must not error: %v", err)
	}
	if second.Contents[0].Text != "original" {
		t.Fatalf("cache entry was polluted by caller mutation: got %q, want %q",
			second.Contents[0].Text, "original")
	}
}
