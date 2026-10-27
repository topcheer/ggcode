//go:build goolm

package im

import (
	"os"
	"strings"
	"testing"
)

// #4464: an image-only outbound message whose upload failed used to
// silently vanish (Send returned nil). The fallback contract: when every
// extracted image failed AND no text remains, the original markdown must
// be sent instead. ExtractImagesFromText already strips the image to ""
// here; the adapters' fallback re-arms remainingText with the original —
// pinned at the extraction seam the adapters rely on.
func TestIssue4464_ImageOnlyExtractionLeavesEmptyRemainder(t *testing.T) {
	// Empty alt (or a bare-URL alt) is the case where the strip removes
	// the whole span — THAT is the silent-drop precondition the adapters
	// guard. A non-empty alt leaves the alt as text (not a drop).
	images, remaining := ExtractImagesFromText("![](https://cdn.example.com/expired.png)")
	if len(images) != 1 {
		t.Fatalf("one image expected, got %d", len(images))
	}
	if strings.TrimSpace(remaining) != "" {
		t.Fatalf("image-only markdown must leave no remainder, got %q", remaining)
	}
	// Multi-image + caption keeps the caption as remainder.
	images2, remaining2 := ExtractImagesFromText("![a](u1) caption ![b](u2)")
	if len(images2) != 2 || strings.TrimSpace(remaining2) != "a caption b" {
		t.Fatalf("caption preservation broken: imgs=%d rem=%q", len(images2), remaining2)
	}
}

// #4465: mattermost seen-dedup must not mark posts that are filtered out.
// Structural pin: the handler marks seen AFTER the empty-text filter, so a
// replayed filtered post never consumes dedup capacity. We pin the
// ordering contract by locating the two statements in the source.
func TestIssue4465_SeenMarkedAfterFilters(t *testing.T) {
	src, err := os.ReadFile("mattermost_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(src)
	markIdx := strings.Index(s, "a.seen[postID] = time.Now()")
	filterIdx := strings.Index(s, `if strings.TrimSpace(messageText) == "" {`)
	allowedIdx := strings.Index(s, "Allowed users check")
	if markIdx < 0 || filterIdx < 0 || allowedIdx < 0 {
		t.Fatal("expected statements not found in mattermost_adapter.go")
	}
	if !(allowedIdx < markIdx && filterIdx < markIdx) {
		t.Fatal("#4465 regression: seen mark must come after allowed-users and empty-text filters")
	}
	// Exactly ONE mark site must remain (the early dedup block must only check).
	if strings.Count(s, "a.seen[postID] = time.Now()") != 1 {
		t.Fatal("expected exactly one seen-mark site after #4465")
	}
}
