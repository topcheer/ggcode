//go:build windows

package image

// Regression probes for #3009: the Windows capture chain must not treat a
// PowerShell exit 0 as proof of success while a stale OutputPath file is
// served as this round's capture, and parse failures must not masquerade as
// empty results.

import (
	"strings"
	"testing"
)

// TestIssue3009_ScriptIsFailFast pins W1: the generated script must set
// $ErrorActionPreference='Stop' and wrap everything in try/catch with exit 1,
// for all three capture modes (window / region / full display).
func TestIssue3009_ScriptIsFailFast(t *testing.T) {
	cases := map[string]ScreenshotOptions{
		"window": {Window: "Notepad"},
		"region": {Region: &ScreenshotRegion{X: 0, Y: 0, Width: 100, Height: 80}},
		"full":   {},
	}
	for name, opts := range cases {
		script := buildWindowsScreenshotScript(`C:\temp\shot.png`, opts)
		if !strings.Contains(script, "$ErrorActionPreference = 'Stop'") {
			t.Fatalf("%s: script must set ErrorActionPreference=Stop", name)
		}
		tryIdx := strings.Index(script, "try {")
		catchIdx := strings.Index(script, "} catch {")
		if tryIdx < 0 || catchIdx < 0 || catchIdx < tryIdx {
			t.Fatalf("%s: script must wrap the body in try/catch", name)
		}
		addType := strings.Index(script, "Add-Type")
		save := strings.Index(script, "$bmp.Save(")
		if addType < tryIdx || save > catchIdx {
			t.Fatalf("%s: try block must cover Add-Type through Save", name)
		}
		if !strings.Contains(script, "exit 1") {
			t.Fatalf("%s: catch must exit 1", name)
		}
	}
}

// TestIssue3009_RegionMustBePositive pins W4: non-positive width/height is
// rejected up front with a root-cause error instead of failing inside
// PowerShell's Bitmap constructor.
func TestIssue3009_RegionMustBePositive(t *testing.T) {
	for _, region := range []*ScreenshotRegion{
		{X: 0, Y: 0, Width: 0, Height: 100},
		{X: 0, Y: 0, Width: 100, Height: 0},
		{X: 0, Y: 0, Width: -5, Height: 100},
	} {
		_, err := CaptureScreen(ScreenshotOptions{Region: region})
		if err == nil {
			t.Fatalf("region %+v must be rejected", region)
		}
		if !strings.Contains(err.Error(), "positive width and height") {
			t.Fatalf("error must name the region root cause, got: %v", err)
		}
	}
}

// TestIssue3009_ParseDisplayInfos pins W2: empty output is a legal empty
// list; valid JSON (array or single object) decodes; polluted stdout errors.
func TestIssue3009_ParseDisplayInfos(t *testing.T) {
	if d, err := parseDisplayInfos([]byte("  \r\n")); err != nil || d != nil {
		t.Fatalf("empty output must be (nil, nil), got (%v, %v)", d, err)
	}
	arr, err := parseDisplayInfos([]byte(`[{"name":"DP-1","x":0,"y":0,"width":1920,"height":1080}]`))
	if err != nil || len(arr) != 1 || arr[0].Name != "DP-1" {
		t.Fatalf("array decode failed: %v %v", arr, err)
	}
	single, err := parseDisplayInfos([]byte(`{"name":"DP-2","x":1920,"y":0,"width":1080,"height":1920}`))
	if err != nil || len(single) != 1 || single[0].Name != "DP-2" {
		t.Fatalf("single-object decode failed: %v %v", single, err)
	}
	if _, err := parseDisplayInfos([]byte("Add-Type : cannot load")); err == nil {
		t.Fatal("polluted stdout must surface as a parse error, not an empty list")
	}
}

// TestIssue3009_ParseWindowInfos mirrors the displays probe for windows.
func TestIssue3009_ParseWindowInfos(t *testing.T) {
	if w, err := parseWindowInfos([]byte("")); err != nil || w != nil {
		t.Fatalf("empty output must be (nil, nil), got (%v, %v)", w, err)
	}
	ws, err := parseWindowInfos([]byte(`[{"id":1,"title":"A","app":"a"},{"id":2,"title":"B","app":"b"}]`))
	if err != nil || len(ws) != 2 {
		t.Fatalf("array decode failed: %v %v", ws, err)
	}
	one, err := parseWindowInfos([]byte(`{"id":3,"title":"C","app":"c"}`))
	if err != nil || len(one) != 1 || one[0].Title != "C" {
		t.Fatalf("single-object decode failed: %v %v", one, err)
	}
	if _, err := parseWindowInfos([]byte("some powershell noise")); err == nil {
		t.Fatal("polluted stdout must surface as a parse error, not 0 windows")
	}
}
