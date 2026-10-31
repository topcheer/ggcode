//go:build darwin

package tool

import "testing"

// #4713 (main defect, high): iPhone 16/17 Pro (402pt -> 1206px) and
// 17 Pro Max (440pt -> 1320px) logical widths were missing from the probe
// table - the scale fell back to (1,1), raw physical-pixel coordinates went
// to AppleScript, taps landed off-screen while reporting success.
func TestIssue4713_ProbeLogicalScaleCovers3xProWidths(t *testing.T) {
	cases := []struct {
		pxW            int
		wantSX, wantSY int
		note           string
	}{
		{1206, 3, 3, "iPhone 16/17 Pro: 402pt @3x"},
		{1320, 3, 3, "iPhone 17 Pro Max: 440pt @3x"},
		{1179, 3, 3, "iPhone 15 Pro: 393pt @3x"},
		{1170, 3, 3, "iPhone 390pt @3x"},
		{1242, 3, 3, "414pt @3x"},
		{1284, 3, 3, "428pt @3x"},
		{1290, 3, 3, "430pt @3x"},
		{750, 2, 2, "375pt @2x"},
		{780, 2, 2, "390pt @2x"},
		{828, 2, 2, "414pt @2x"},
		{640, 2, 2, "320pt @2x"},
		{2048, 2, 2, "iPad 1024pt @2x"},
		{744, 1, 1, "744pt @1x (iPad mini landscape-era @1)"},
		{1234, 1, 1, "no known width divides it - fallback"},
		{2049, 1, 1, "divides 683 but s>3 filtered - fallback"},
	}
	for _, c := range cases {
		sx, sy := probeLogicalScale(c.pxW)
		if sx != c.wantSX || sy != c.wantSY {
			t.Errorf("probeLogicalScale(%d)=%d,%d want %d,%d (%s)", c.pxW, sx, sy, c.wantSX, c.wantSY, c.note)
		}
	}
}

// The table itself stays ascending and free of duplicates - a probe-order
// assumption (first ascending match wins) that a bad edit would silently break.
func TestIssue4713_LogicalWidthsAscending(t *testing.T) {
	for i := 1; i < len(iosLogicalWidths); i++ {
		if iosLogicalWidths[i] <= iosLogicalWidths[i-1] {
			t.Fatalf("iosLogicalWidths must stay strictly ascending: %d at %d", iosLogicalWidths[i], i)
		}
	}
}
