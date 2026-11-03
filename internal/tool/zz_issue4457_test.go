//go:build goolm

package tool

import (
	"context"
	"strings"
	"testing"
)

func TestIssue4457_ParseAndroidUIXMLNilRootNoPanic(t *testing.T) {

	malformed := `<node index="0" text="" resource-id="" class=""/><node index="1" text="" resource-id="" class="x" content-desc="ok" package="p" checkable="false" checked="false" clickable="false" enabled="true" focusable="true" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" bounds="[0,0][100,200]"/>`
	root := parseAndroidUIXML(malformed)
	if root == nil {
		t.Fatal("expected valid fallback root from second node, got nil")
	}

	allBad := `<node index="0" text="" resource-id="" class=""/>`
	if root := parseAndroidUIXML(allBad); root != nil {
		t.Fatalf("expected nil root for fully malformed dump, got %+v", root)
	}

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

	res, err := b.swipe(context.Background(), "", "@e3", 0, 0, intPtr(0), intPtr(0))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("ref-only swipe must be refused (#4457), got: %s", res.Content)
	}

	res, err = b.swipe(context.Background(), "", "", 0, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("all-zero swipe must be refused (#4457), got: %s", res.Content)
	}
}

func intPtr(v int) *int { return &v }
