package mcp

import (
	"context"
	"testing"
)

// #3041: a cache-hit ReadResource must hand out a deep-enough copy - the
// returned Contents slice may not share its backing array with the cache
// entry, or a caller mutating its copy pollutes every later read within the
// TTL. The tools/prompts paths already clone via cloneCachedSlice; this is
// the resource-read twin.
func TestReadResourceCacheHitClonesContents(t *testing.T) {
	client := &Client{name: "clonecheck"}
	original := ReadResourceResult{
		Contents: []ResourceContent{{URI: "file:///a.txt", Text: "original"}},
	}
	client.storeListingsCache(cacheResourceRead, "file:///a.txt", original, []CacheableResult{{TTLms: 60000}})

	// Cache-hit path: no server needed, the get() must succeed before any RPC.
	first, err := client.ReadResource(context.Background(), "file:///a.txt")
	if err != nil {
		t.Fatalf("cache-hit read must not error: %v", err)
	}
	if len(first.Contents) != 1 || first.Contents[0].Text != "original" {
		t.Fatalf("unexpected cached content: %+v", first.Contents)
	}

	// Mutate the caller's copy — element write through the shared backing
	// array is exactly the pollution path from the issue.
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
