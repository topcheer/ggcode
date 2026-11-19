//go:build darwin

package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeSimRunner fakes the osascript process-list probe and the `open -a
// DeviceHub` fallback so the three probe branches are testable without a
// real simulator frontend.
func fakeSimRunner(processes string, probeErr error, openErr error) simCmdRunner {
	return func(ctx context.Context, timeout time.Duration, name string, args ...string) (string, string, error) {
		switch name {
		case "osascript":
			if probeErr != nil {
				return "", "probe failed", probeErr
			}
			return processes, "", nil
		case "open":
			if openErr != nil {
				return "", "open failed", openErr
			}
			return "", "", nil
		}
		return "", "", errors.New("unexpected command: " + name)
	}
}

func TestSimFrontendAppProbeBranches(t *testing.T) {
	cases := []struct {
		name      string
		processes string
		probeErr  error
		openErr   error
		want      string
		wantOpen  bool // whether the DeviceHub launch fallback should fire
	}{
		{
			name:      "DeviceHub running wins",
			processes: "Finder, DeviceHub, Safari",
			want:      "DeviceHub",
		},
		{
			name:      "only Simulator running (older macOS)",
			processes: "Finder, Simulator, Terminal",
			want:      "Simulator",
		},
		{
			name:      "neither running, DeviceHub launch succeeds",
			processes: "Finder, Terminal",
			want:      "DeviceHub",
			wantOpen:  true,
		},
		{
			name:      "neither running, DeviceHub launch fails, silent Simulator fallback",
			processes: "Finder, Terminal",
			openErr:   errors.New("unable to find application named DeviceHub"),
			want:      "Simulator",
			wantOpen:  true,
		},
		{
			name:     "probe error falls through to open branch",
			probeErr: errors.New("osascript: not authorized"),
			openErr:  errors.New("no DeviceHub"),
			want:     "Simulator",
			wantOpen: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			openCalled := false
			run := func(ctx context.Context, timeout time.Duration, name string, args ...string) (string, string, error) {
				if name == "open" {
					openCalled = true
				}
				return fakeSimRunner(tc.processes, tc.probeErr, tc.openErr)(ctx, timeout, name, args...)
			}
			got := simFrontendApp(context.Background(), run)
			if got != tc.want {
				t.Fatalf("simFrontendApp() = %q, want %q", got, tc.want)
			}
			if openCalled != tc.wantOpen {
				t.Fatalf("open fallback called = %v, want %v", openCalled, tc.wantOpen)
			}
		})
	}
}

func TestSimFrontendAppNilRunnerUsesDefault(t *testing.T) {
	// Must not panic; on hosts without permission this degrades to the
	// Simulator fallback path.
	got := simFrontendApp(context.Background(), nil)
	if got != "DeviceHub" && got != "Simulator" {
		t.Fatalf("unexpected frontend name %q", got)
	}
}

func TestMobileAppleScriptInterpolatesFrontendApp(t *testing.T) {
	const wantActivate = `tell application "DeviceHub" to activate`

	if s := tapAppleScript("DeviceHub", 120, 240); !strings.Contains(s, wantActivate) {
		t.Errorf("tap script missing DeviceHub activation:\n%s", s)
	} else if strings.Contains(s, `"Simulator"`) {
		t.Errorf("tap script still references Simulator:\n%s", s)
	}

	if s := typeAppleScript("DeviceHub", `he said \"hi\"`); !strings.Contains(s, wantActivate) {
		t.Errorf("type script missing DeviceHub activation:\n%s", s)
	} else if !strings.Contains(s, `keystroke "he said \"hi\""`) {
		t.Errorf("type script lost escaped text:\n%s", s)
	}

	if s := swipeAppleScript("DeviceHub", 0, 200, 300, 200); !strings.Contains(s, wantActivate) {
		t.Errorf("swipe script missing DeviceHub activation:\n%s", s)
	} else if !strings.Contains(s, "mouse move to {300, 200}") {
		t.Errorf("swipe script missing final interpolated mouse move:\n%s", s)
	}

	for _, key := range []string{"home", "enter", "escape", "volume-up"} {
		s, ok := pressAppleScript("DeviceHub", key)
		if !ok {
			t.Errorf("press %s: expected supported key", key)
			continue
		}
		if !strings.Contains(s, wantActivate) {
			t.Errorf("press %s script missing DeviceHub activation:\n%s", key, s)
		}
		if strings.Contains(s, `"Simulator"`) {
			t.Errorf("press %s script still references Simulator:\n%s", key, s)
		}
	}

	if _, ok := pressAppleScript("DeviceHub", "back"); ok {
		t.Error("press back: expected unsupported (ok=false)")
	}

	// Old-macOS fallback: the same builders must still emit Simulator when
	// the probe resolved the legacy frontend.
	if s := tapAppleScript("Simulator", 1, 2); !strings.Contains(s, `tell application "Simulator" to activate`) {
		t.Errorf("tap script lost Simulator fallback:\n%s", s)
	}
}
