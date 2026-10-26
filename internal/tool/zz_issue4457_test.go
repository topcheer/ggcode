//go:build goolm

package tool

// #4457 regression: three mobile-backend guards.
// 1. parseAndroidUIXML: malformed uiautomator XML (first node unparseable)
//    must not panic on nil root.
// 2. android typeText: literal %s in user text must be refused (adb input
//    decodes %s to a space on the device - silent corruption).
// 3. iOS swipe: ref-only and all-zero swipes must be refused instead of
//    running (0,0)->(0,0) and reporting success.

import (
	"context"
	"strings"
	"testing"
)

func TestIssue4457_ParseAndroidUIXMLNilRootNoPanic(t *testing.T) {
	// First node: no label, no bounds -> parseAndroidNode returns nil.
	// Remaining node is valid, so the fixed code adopts it as root.
	malformed := `<node index="0" text="" resource-id="" class=""/><node index="1" text="" resource-id="" class="x" content-desc="ok" package="p" checkable="false" checked="false" clickable="false" enabled="true" focusable="true" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" bounds="[0,0][100,200]"/>`
	root := parseAndroidUIXML(malformed) // must not panic
	if root == nil {
		t.Fatal("expected valid fallback root from second node, got nil")
	}

	// All nodes unparseable -> nil, still no panic.
	allBad := `<node index="0" text="" resource-id="" class=""/>`
	if root := parseAndroidUIXML(allBad); root != nil {
		t.Fatalf("expected nil root for fully malformed dump, got %+v", root)
	}

	// Sanity: a normal dump still parses.
	normal := `<node index="0" text="" resource-id="" class="h" content-desc="root" package="p" bounds="[0,0][200,400]"/>`
	root = parseAndroidUIXML(normal)
	if root == nil {
		t.Fatal("normal dump should parse")
	}
}

func TestIssue4457_AndroidTypeTextLiteralPercentS(t *testing.T) {
	a := &androidBackend{}
	res, err := a.typeText(context.Background(), "", "", "printf %s", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("literal %%s must be refused, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "%s") {
		t.Fatalf("error should explain the %%s decoding hazard, got: %s", res.Content)
	}
}

func TestIssue4457_IOSSwipeRefAndZeroGuards(t *testing.T) {
	b := &iosBackend{}
	// ref-only swipe: must refuse before touching osascript.
	res, err := b.swipe(context.Background(), "", "@e3", 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("ref-only swipe must be refused (#4457), got: %s", res.Content)
	}
	// all-zero coordinates without ref: must refuse too.
	res, err = b.swipe(context.Background(), "", "", 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("all-zero swipe must be refused (#4457), got: %s", res.Content)
	}
}
