//go:build darwin

package image

import (
	"strings"
	"testing"
)

// TestIssue2999_ParseNSScreenOutput pins the logical-coordinate NSScreen
// parser: coordinates arrive verbatim (NO pixel/logical scaling) because the
// Swift snippet already converts to the top-left-origin logical coordinate
// system screencapture -R expects (#2999: the system_profiler path mixed VSA
// logical origins with _spdisplays_resolution pixel sizes; Y-axis origin
// conversion happens Swift-side: yTop = primaryMaxY - (minY + height)).
func TestIssue2999_ParseNSScreenOutput(t *testing.T) {
	out := strings.Join([]string{
		"1\t1\t0\t0\t1512\t982\tBuilt-in Retina Display",
		"2\t0\t-1920\t1512\t1920\t1080\tExternal",
	}, "\n")
	displays := parseNSScreenOutput(out)
	if len(displays) != 2 {
		t.Fatalf("expected 2 displays, got %d", len(displays))
	}
	d1 := displays[0]
	if !d1.IsPrimary || d1.Index != 1 || d1.X != 0 || d1.Y != 0 {
		t.Fatalf("primary display fields wrong: %+v", d1)
	}
	// Retina logical size must survive verbatim - NOT doubled to 3024x1964.
	if d1.Width != 1512 || d1.Height != 982 {
		t.Fatalf("logical width/height must be kept verbatim, got %dx%d", d1.Width, d1.Height)
	}
	if d1.Name != "Built-in Retina Display" {
		t.Fatalf("name not parsed: %q", d1.Name)
	}
	// Secondary display: negative origin (left of primary) preserved.
	d2 := displays[1]
	if d2.IsPrimary {
		t.Fatal("second display must not be primary")
	}
	if d2.X != -1920 || d2.Y != 1512 || d2.Width != 1920 || d2.Height != 1080 {
		t.Fatalf("secondary display coords wrong: %+v", d2)
	}
}

// TestIssue2999_ParseNSScreenOutputSkipsMalformed pins defensive parsing:
// malformed lines are skipped without aborting the whole enumeration.
func TestIssue2999_ParseNSScreenOutputSkipsMalformed(t *testing.T) {
	out := "garbage line\n1\t1\t0\t0\t100\t100\tOK\nshort\tline\n"
	displays := parseNSScreenOutput(out)
	if len(displays) != 1 {
		t.Fatalf("expected 1 parsed display, got %d", len(displays))
	}
	if displays[0].Name != "OK" {
		t.Fatalf("wrong survivor: %+v", displays[0])
	}
}

// TestIssue2999_ParseNSScreenOutputEmpty pins the empty-output contract used
// by listDisplaysNSScreen to decide fallback to the system_profiler path.
func TestIssue2999_ParseNSScreenOutputEmpty(t *testing.T) {
	if got := parseNSScreenOutput(""); got != nil {
		t.Fatalf("empty input must return nil, got %+v", got)
	}
	if got := parseNSScreenOutput("   \n\n"); got != nil {
		t.Fatalf("whitespace input must return nil, got %+v", got)
	}
}
