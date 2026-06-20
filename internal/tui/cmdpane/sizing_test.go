package cmdpane

import "testing"

func TestDeterminePlacement(t *testing.T) {
	tests := []struct {
		name        string
		cols, rows  int
		pixW, pixH  int
		wantDir     string
		wantSizeGt0 bool
		wantSizeLe  int // size must be <= this (30% cap)
	}{
		// Landscape (no pixels) — cols-based right pane
		{"both large landscape", 200, 100, 0, 0, "right", true, 60},
		{"width only landscape", 120, 30, 0, 0, "right", true, 36},
		{"neither", 40, 20, 0, 0, "", false, 0},
		{"boundary cols=81", 81, 40, 0, 0, "right", true, 24},
		{"boundary cols=80", 80, 40, 0, 0, "", false, 0},
		{"boundary rows=51", 60, 51, 0, 0, "bottom", true, 15},
		{"boundary rows=50", 60, 50, 0, 0, "", false, 0},

		// Portrait detected via cells (no pixels): rows > cols
		{"cell portrait wide enough", 100, 120, 0, 0, "bottom", true, 36},
		{"cell portrait narrow", 60, 100, 0, 0, "bottom", true, 30},

		// Portrait detected via pixels even though cols > rows.
		// This is the key fix: a physically tall window has cols > rows
		// because character cells are taller than wide (~1:2 ratio).
		{"pixel portrait cols>rows", 100, 70, 800, 1200, "bottom", true, 21},
		{"pixel portrait cols>>rows", 200, 80, 600, 1400, "bottom", true, 24},

		// Landscape confirmed by pixels even though rows > cols.
		// A wide window can have rows > cols if font is very wide.
		// cols must be > 80 to qualify for right pane.
		{"pixel landscape rows>cols", 100, 120, 1200, 800, "right", true, 30},

		// Square pixels → not portrait → cols-based
		{"square pixels cols>80", 100, 100, 800, 800, "right", true, 30},
		{"square pixels cols<80 rows>50", 60, 80, 600, 600, "bottom", true, 24},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := DeterminePlacement(tc.cols, tc.rows, tc.pixW, tc.pixH)
			if p.Direction != tc.wantDir {
				t.Errorf("Direction = %q, want %q (cols=%d rows=%d pixW=%d pixH=%d)",
					p.Direction, tc.wantDir, tc.cols, tc.rows, tc.pixW, tc.pixH)
			}
			if tc.wantSizeGt0 && p.Size <= 0 {
				t.Errorf("Size = %d, want > 0", p.Size)
			}
			if !tc.wantSizeGt0 && p.Size != 0 {
				t.Errorf("Size = %d, want 0 for inactive placement", p.Size)
			}
			if tc.wantSizeLe > 0 && p.Size > tc.wantSizeLe {
				t.Errorf("Size = %d, exceeds 30%% cap (%d)", p.Size, tc.wantSizeLe)
			}
		})
	}
}

func TestDeterminePlacement30PercentCap(t *testing.T) {
	// Verify the 30% cap for right pane (landscape, no pixels)
	for _, w := range []int{100, 120, 200, 300, 500} {
		p := DeterminePlacement(w, 40, 0, 0) // rows small → not portrait
		if p.Direction != "right" {
			t.Errorf("cols=%d: expected right, got %s", w, p.Direction)
			continue
		}
		maxAllowed := w * 30 / 100
		if p.Size > maxAllowed {
			t.Errorf("cols=%d: Size=%d exceeds 30%% cap (%d)", w, p.Size, maxAllowed)
		}
	}
	// Verify for heights (narrow terminal, no pixels)
	for _, h := range []int{51, 60, 80, 100, 200} {
		p := DeterminePlacement(40, h, 0, 0)
		if p.Direction != "bottom" {
			t.Errorf("rows=%d: expected bottom, got %s", h, p.Direction)
			continue
		}
		maxAllowed := h * 30 / 100
		if p.Size > maxAllowed {
			t.Errorf("rows=%d: Size=%d exceeds 30%% cap (%d)", h, p.Size, maxAllowed)
		}
	}
}

func TestPlacementIsActive(t *testing.T) {
	if (Placement{}).IsActive() {
		t.Error("zero-value Placement should not be active")
	}
	if !(Placement{Direction: "right", Size: 20}).IsActive() {
		t.Error("Placement{right} should be active")
	}
}

func TestShouldRecreatePane(t *testing.T) {
	tests := []struct {
		name string
		cur  Placement
		want Placement
		yes  bool
	}{
		{"same direction + minor size change", Placement{"right", 60}, Placement{"right", 55}, false},
		{"same direction + huge size change", Placement{"right", 60}, Placement{"right", 20}, true},
		{"direction changed right→bottom", Placement{"right", 60}, Placement{"bottom", 24}, true},
		{"direction changed bottom→right", Placement{"bottom", 24}, Placement{"right", 60}, true},
		{"same direction + same size", Placement{"right", 60}, Placement{"right", 60}, false},
		{"current empty, want active", Placement{}, Placement{"right", 60}, true},
		{"both empty", Placement{}, Placement{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &Manager{curPlacement: tc.cur}
			got := mgr.shouldRecreatePane(tc.want)
			if got != tc.yes {
				t.Errorf("shouldRecreatePane(%+v → %+v) = %v, want %v",
					tc.cur, tc.want, got, tc.yes)
			}
		})
	}
}
