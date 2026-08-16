//go:build darwin

package image

import (
	"strings"
	"testing"
)

// Bug B (#555): ListDisplays previously reused the GPU-unit index, so a
// single GPU driving two monitors reported both as Index:1 IsPrimary:true,
// and X/Y were never populated.

const issue555SPDisplaysSample = `{
  "SPDisplaysDataType" : [
    {
      "_name" : "Apple M2 Ultra",
      "spdisplays_ndrvs" : [
        {
          "_name" : "DELL U2720Q",
          "_spdisplays_resolution" : "2560 x 1440 @ 60.00Hz",
          "spdisplays_main" : "spdisplays_yes",
          "spdisplays_mirror" : "spdisplays_off",
          "_spdisplays_display-vsa" : {
            "OffsetX" : 0,
            "OffsetY" : 0
          }
        },
        {
          "_name" : "LG UltraFine",
          "_spdisplays_resolution" : "1920 x 1080 @ 60.00Hz",
          "spdisplays_mirror" : "spdisplays_off",
          "_spdisplays_display-vsa" : {
            "OffsetX" : 2560,
            "OffsetY" : -1440
          }
        }
      ],
      "sppci_device_type" : "spdisplays_gpu"
    }
  ]
}`

func TestIssue555ParseSPDisplaysRenumbersAcrossGPUs(t *testing.T) {
	displays, err := parseSPDisplaysJSON([]byte(issue555SPDisplaysSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(displays) != 2 {
		t.Fatalf("got %d displays, want 2", len(displays))
	}
	// Output units must be renumbered 1..N, not GPU-indexed.
	if displays[0].Index != 1 || displays[1].Index != 2 {
		t.Errorf("Indexes = %d,%d; want 1,2", displays[0].Index, displays[1].Index)
	}
	// Exactly one primary, and it is the spdisplays_main entry.
	primaries := 0
	for _, d := range displays {
		if d.IsPrimary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Fatalf("got %d primaries, want 1", primaries)
	}
	if !displays[0].IsPrimary {
		t.Error("display with spdisplays_main=yes must be primary")
	}
	if displays[0].Width != 2560 || displays[0].Height != 1440 {
		t.Errorf("primary dims = %dx%d; want 2560x1440", displays[0].Width, displays[0].Height)
	}
	// X/Y from _spdisplays_display-vsa, including negative origins.
	if displays[1].X != 2560 || displays[1].Y != -1440 {
		t.Errorf("secondary origin = %d,%d; want 2560,-1440", displays[1].X, displays[1].Y)
	}
}

func TestIssue555ParseSPDisplaysSingleGPUDualMonitors(t *testing.T) {
	// The exact configuration from the issue: one GPU entry, two displays.
	// Old code: both Index:1, both IsPrimary:true.
	sample := `{"SPDisplaysDataType":[{"_name":"GPU","spdisplays_ndrvs":[
		{"_name":"A","_spdisplays_resolution":"1920 x 1080 @ 60.00Hz"},
		{"_name":"B","_spdisplays_resolution":"1920 x 1080 @ 60.00Hz"}
	]}]}`
	displays, err := parseSPDisplaysJSON([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(displays) != 2 {
		t.Fatalf("got %d displays, want 2", len(displays))
	}
	if displays[0].Index != 1 || displays[1].Index != 2 {
		t.Errorf("Indexes = %d,%d; want distinct 1,2", displays[0].Index, displays[1].Index)
	}
	// No spdisplays_main field: first display is the fallback primary, and
	// only that one.
	if displays[0].IsPrimary == displays[1].IsPrimary {
		t.Errorf("IsPrimary = %v,%v; want exactly the first true", displays[0].IsPrimary, displays[1].IsPrimary)
	}
	if !displays[0].IsPrimary {
		t.Error("first display should be fallback primary")
	}
}

func TestIssue555ListDisplaysLiveSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("live system_profiler call")
	}
	displays, err := ListDisplays()
	if err != nil {
		t.Skipf("system_profiler unavailable: %v", err)
	}
	seen := map[int]int{}
	primaries := 0
	for _, d := range displays {
		seen[d.Index]++
		if d.IsPrimary {
			primaries++
		}
	}
	for idx, n := range seen {
		if n != 1 {
			t.Errorf("display Index %d appears %d times (GPU-unit index bug)", idx, n)
		}
	}
	if primaries > 1 {
		t.Errorf("got %d primary displays, want at most 1", primaries)
	}
	if len(displays) > 1 {
		names := ""
		for _, d := range displays {
			names += d.Name + ","
		}
		t.Logf("live displays: %s", strings.TrimSuffix(names, ","))
	}
}
