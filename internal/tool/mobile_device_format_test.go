package tool

// formatSnapshot / findElementByID / imageResult coverage (sa-141).

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestFormatSnapshotSmallTreeSa141(t *testing.T) {
	root := &uiElement{Type: "window", Children: []*uiElement{
		{Type: "button", Label: "OK", Rect: &uiRect{X: 0, Y: 0, Width: 10, Height: 10}},
		{Type: "text", Label: "hi", Rect: &uiRect{X: 5, Y: 5, Width: 20, Height: 8}, Value: "v"},
	}}
	out := formatSnapshot(root, "device: test")
	if !strings.HasPrefix(out, "device: test\n\n") {
		t.Fatalf("device header missing: %.60s", out)
	}
	if !strings.Contains(out, "@e1 [button] \"OK\"") {
		t.Fatalf("@e1 stamp missing: %s", out)
	}
	if !strings.Contains(out, "@e2") {
		t.Fatalf("@e2 stamp missing: %s", out)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("unexpected truncation note: %s", out)
	}
	if !strings.Contains(out, "rect={0,0,10,10}") {
		t.Fatalf("rect render missing: %s", out)
	}
	if !strings.Contains(out, "value=\"v\"") {
		t.Fatalf("value render missing: %s", out)
	}
	if root.Children[0].ID != "@e1" {
		t.Fatalf("element ID not stamped back: %q", root.Children[0].ID)
	}
}

func TestFormatSnapshotElementCapSa141(t *testing.T) {
	// 501 rect-bearing elements exceed the 500-element cap: annotation must fire.
	children := make([]*uiElement, 0, 501)
	for i := 0; i < 501; i++ {
		children = append(children, &uiElement{Type: "button", Label: "x", Rect: &uiRect{X: i, Y: i, Width: 1, Height: 1}})
	}
	out := formatSnapshot(&uiElement{Type: "window", Children: children}, "")
	if !strings.Contains(out, "reached the 500-element limit") {
		t.Fatal("element-cap annotation missing")
	}
}

func TestFormatSnapshotByteCapSa141(t *testing.T) {
	// <500 elements but huge labels exceed the 30KB byte cap.
	children := make([]*uiElement, 0, 300)
	label := strings.Repeat("L", 200)
	for i := 0; i < 300; i++ {
		children = append(children, &uiElement{Type: "text", Label: label, Rect: &uiRect{X: i, Y: 0, Width: 1, Height: 1}})
	}
	out := formatSnapshot(&uiElement{Type: "window", Children: children}, "")
	if len(out) > 31*1024 {
		t.Fatalf("byte cap not applied: %d bytes", len(out))
	}
	if !strings.Contains(out, "bytes total") {
		t.Fatal("byte-cap annotation missing")
	}
}

func TestFindElementByIDSa141(t *testing.T) {
	if got := findElementByID(nil, "@e1"); got != nil {
		t.Fatalf("findElementByID(nil) = %+v", got)
	}
	deep := &uiElement{ID: "@root", Children: []*uiElement{
		{ID: "@e1", Children: []*uiElement{{ID: "@e2"}}},
	}}
	if got := findElementByID(deep, "@e2"); got == nil || got.ID != "@e2" {
		t.Fatalf("nested lookup = %+v", got)
	}
	if got := findElementByID(deep, "@nope"); got != nil {
		t.Fatalf("missing lookup = %+v", got)
	}
}

func TestImageResultSa141(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G'}
	r := imageResult(png, "png", 0, "shot")
	if r.Content != "shot" || len(r.Images) != 1 || r.Images[0].MIME != "image/png" {
		t.Fatalf("png result = %+v", r)
	}
	if _, err := base64.StdEncoding.DecodeString(r.Images[0].Base64); err != nil {
		t.Fatalf("base64 payload invalid: %v", err)
	}
	r = imageResult(png, "jpeg", 0, "shot")
	if r.Images[0].MIME != "image/jpeg" {
		t.Fatalf("jpeg mime = %q", r.Images[0].MIME)
	}
}

func TestFormatElementNilAndCapSa141(t *testing.T) {
	var sb strings.Builder
	counter := 0
	formatElement(&sb, nil, 0, &counter, 5)
	if sb.Len() != 0 {
		t.Fatalf("nil element wrote output: %q", sb.String())
	}
	// Counter at cap: no further output.
	counter = 5
	formatElement(&sb, &uiElement{Type: "button", Label: "late", Rect: &uiRect{}}, 0, &counter, 5)
	if sb.Len() != 0 {
		t.Fatalf("capped element wrote output: %q", sb.String())
	}
	_ = fmt.Sprint()
}
