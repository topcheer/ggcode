//go:build darwin

package tool

// ios backend coverage (sa-141): pure escape helper, graceful fallbacks when
// no simulator device exists. All simctl invocations are read-only.

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestEscapeAppleScriptStringSa141(t *testing.T) {
	cases := map[string]string{
		"plain":         "plain",
		"a\"b":          "a\\\"b",
		"back\\\\slash": "back\\\\\\\\slash",
		"":              "",
	}
	for in, want := range cases {
		if got := escapeAppleScriptString(in); got != want {
			t.Errorf("escapeAppleScriptString(%q) = %q, want %q", in, got, want)
		}
	}
	// Order matters: quote-escaping must not double the backslash it adds.
	if got := escapeAppleScriptString("\""); got != "\\\"" {
		t.Fatalf("quote escape = %q, want backslash-quote", got)
	}
}

func TestIOSDefaultDeviceNoSimulatorSa141(t *testing.T) {
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not installed")
	}
	b := newIOSBackend()
	// Read-only listing; empty string when no booted device.
	got := b.defaultDevice()
	t.Logf("defaultDevice = %q", got)
}

func TestIOSDevicesResultSa141(t *testing.T) {
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not installed")
	}
	b := newIOSBackend()
	r, err := b.devices(context.Background())
	if err != nil {
		t.Fatalf("devices returned err %v (want nil, errors carried in Result)", err)
	}
	if r.Content == "" {
		t.Fatal("devices returned empty content")
	}
	t.Logf("devices result: %.120s", r.Content)
}

func TestIOSScaleFallbackSa141(t *testing.T) {
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not installed")
	}
	b := newIOSBackend()
	s, _ := b.scaleForDevice("no-such-udid-sa141")
	if s != 1 {
		t.Fatalf("scaleForDevice(bogus) = %d, want fallback 1", s)
	}
}

func TestIOSBackendSnapshotGuardSa141(t *testing.T) {
	b := newIOSBackend()
	r, err := b.snapshot(context.Background(), "no-such-udid-sa141")
	if err != nil {
		t.Fatalf("snapshot err = %v", err)
	}
	// Error or empty listing both acceptable; must not panic and must say something.
	if r.Content == "" {
		t.Fatal("snapshot returned empty content")
	}
	t.Logf("snapshot result: %.120s", strings.ReplaceAll(r.Content, "\n", " "))
}
