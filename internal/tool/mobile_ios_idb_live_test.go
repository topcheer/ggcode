//go:build darwin

package tool

// Live harness against a booted simulator. Skipped unless GGCODE_LIVE_IOS_UDID
// names a booted UDID; it exercises the real idb path (connect, describe-all,
// tap by @eN ref) and writes a screenshot to GGCODE_LIVE_IOS_SHOT when set.
//
//	GGCODE_LIVE_IOS_UDID=<udid> GGCODE_LIVE_IOS_SHOT=/tmp/x.png \
//	  go test -tags goolm -run TestLiveIOSIDB -v ./internal/tool/

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveIOSIDB(t *testing.T) {
	udid := os.Getenv("GGCODE_LIVE_IOS_UDID")
	if udid == "" {
		t.Skip("set GGCODE_LIVE_IOS_UDID to a booted simulator to run")
	}
	t.Setenv("GGCODE_MOBILE_NO_INSTALL", "1")
	b := newIOSBackend()
	if b == nil {
		t.Fatal("iOS backend unavailable (xcrun missing)")
	}
	// The real locator finds idb on PATH; the test skips below when absent
	// (auto-install is disabled by the env set above).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := b.snapshot(ctx, udid)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("=== snapshot ===\n" + firstLines(res.Content, 40))
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "Driver: idb") {
		t.Skip("idb not available on this machine; fallback path verified by unit tests")
	}

	// Find the theme toggle in the snapshot text and tap it by ref.
	ref := ""
	for _, line := range strings.Split(res.Content, "\n") {
		if strings.Contains(line, "Theme:") {
			if i := strings.Index(line, "@e"); i >= 0 {
				ref = line[i:]
				if j := strings.IndexAny(ref, " \t"); j >= 0 {
					ref = ref[:j]
				}
			}
			break
		}
	}
	if ref == "" {
		t.Skip("no theme control in the current screen")
	}
	if res, _ := b.tap(ctx, udid, ref, 0, 0); res.IsError {
		t.Fatalf("tap %s: %s", ref, res.Content)
	}
	fmt.Println("tapped", ref)
	time.Sleep(700 * time.Millisecond)

	shot, _ := b.screenshot(ctx, udid, "png", 85, true)
	if shot.IsError {
		t.Fatal(shot.Content)
	}
	if out := os.Getenv("GGCODE_LIVE_IOS_SHOT"); out != "" {
		img, _ := b.screenshot(ctx, udid, "png", 85, false)
		if len(img.Images) == 0 {
			t.Fatal("no image returned")
		}
		data, err := base64.StdEncoding.DecodeString(img.Images[0].Base64)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Println("screenshot written to", out)
	}
	// Toggle back so the device is left as found.
	if res, _ := b.tap(ctx, udid, ref, 0, 0); res.IsError {
		t.Fatalf("tap back %s: %s", ref, res.Content)
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// TestLiveIOSLightTheme switches the app to the Light palette through the
// theme menu (snapshot -> tap by ref -> snapshot -> tap by ref), saves a
// screenshot and switches back. Gated by GGCODE_LIVE_IOS_LIGHT=1 in addition
// to GGCODE_LIVE_IOS_UDID.
func TestLiveIOSLightTheme(t *testing.T) {
	udid := os.Getenv("GGCODE_LIVE_IOS_UDID")
	if udid == "" || os.Getenv("GGCODE_LIVE_IOS_LIGHT") != "1" {
		t.Skip("set GGCODE_LIVE_IOS_UDID and GGCODE_LIVE_IOS_LIGHT=1 to run")
	}
	t.Setenv("GGCODE_MOBILE_NO_INSTALL", "1")
	b := newIOSBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	findRef := func(needle string) string {
		res, err := b.snapshot(ctx, udid)
		if err != nil || res.IsError {
			t.Fatalf("snapshot: %v %s", err, res.Content)
		}
		for _, line := range strings.Split(res.Content, "\n") {
			if strings.Contains(line, needle) && strings.Contains(line, "@e") {
				i := strings.Index(line, "@e")
				ref := line[i:]
				if j := strings.IndexAny(ref, " \t"); j >= 0 {
					ref = ref[:j]
				}
				return ref
			}
		}
		return ""
	}
	themeBtn := findRef("Theme:")
	if themeBtn == "" {
		t.Skip("no theme control on the current screen")
	}
	if res, _ := b.tap(ctx, udid, themeBtn, 0, 0); res.IsError {
		t.Fatal(res.Content)
	}
	time.Sleep(600 * time.Millisecond)
	light := findRef("\"Light\"")
	if light == "" {
		t.Fatal("Light menu item not found after opening the theme menu")
	}
	if res, _ := b.tap(ctx, udid, light, 0, 0); res.IsError {
		t.Fatal(res.Content)
	}
	time.Sleep(900 * time.Millisecond)
	img, _ := b.screenshot(ctx, udid, "png", 85, false)
	if len(img.Images) == 0 {
		t.Fatal("no screenshot")
	}
	if out := os.Getenv("GGCODE_LIVE_IOS_SHOT"); out != "" {
		data, _ := base64.StdEncoding.DecodeString(img.Images[0].Base64)
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Println("light screenshot written to", out)
	}
	// Restore Midnight.
	themeBtn = findRef("Theme:")
	if themeBtn == "" {
		t.Fatal("theme control gone after switch")
	}
	if res, _ := b.tap(ctx, udid, themeBtn, 0, 0); res.IsError {
		t.Fatal(res.Content)
	}
	time.Sleep(600 * time.Millisecond)
	mid := findRef("\"Midnight\"")
	if mid == "" {
		t.Fatal("Midnight menu item not found")
	}
	if res, _ := b.tap(ctx, udid, mid, 0, 0); res.IsError {
		t.Fatalf("could not restore Midnight: %s", res.Content)
	}
}
