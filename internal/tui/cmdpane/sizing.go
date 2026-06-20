// Package cmdpane manages a persistent tmux split pane that mirrors
// command execution output in real time.
package cmdpane

// Placement describes where the command pane should be created.
type Placement struct {
	Direction string // "right", "bottom", or "" (none)
	Size      int    // absolute size of the pane in cells (columns or rows)
}

const (
	maxPercentage = 30 // pane must not exceed 30% of the dimension

	widthThreshold  = 80 // minimum window width for a right-side pane
	heightThreshold = 50 // minimum window height for a bottom pane
)

// DeterminePlacement decides pane placement based on terminal dimensions.
//
// cols/rows are character-cell counts (from tmux or TIOCGWINSZ ws_col/ws_row).
// pixW/pixH are pixel dimensions from TIOCGWINSZ (ws_xpixel/ws_ypixel).
// Pass 0 for pixW/pixH when pixel data is unavailable.
//
// Rules (in priority order):
//   - physically portrait (pixH > pixW, or rows>cols when pixels unavailable) → bottom
//   - cols > 80  → right pane, 30% of cols
//   - rows > 50  → bottom pane, 30% of rows
//   - neither    → no pane (zero-value Placement)
//
// Why pixels matter: character cells are taller than they are wide (roughly
// 1:2 ratio). A visually-portrait terminal can still have cols > rows.
// Pixel dimensions correctly identify the physical orientation.
func DeterminePlacement(cols, rows, pixW, pixH int) Placement {
	// Detect portrait using pixel dimensions when available.
	// Fall back to cell counts (less reliable due to non-square cells).
	portrait := false
	if pixW > 0 && pixH > 0 {
		portrait = pixH > pixW
	} else {
		portrait = rows > cols
	}

	// Portrait orientation: always place at bottom.
	if portrait && rows > heightThreshold {
		size := rows * maxPercentage / 100
		if size < 1 {
			size = 1
		}
		return Placement{Direction: "bottom", Size: size}
	}
	if cols > widthThreshold {
		size := cols * maxPercentage / 100
		if size < 1 {
			size = 1
		}
		return Placement{Direction: "right", Size: size}
	}
	if rows > heightThreshold {
		size := rows * maxPercentage / 100
		if size < 1 {
			size = 1
		}
		return Placement{Direction: "bottom", Size: size}
	}
	return Placement{}
}

// IsActive returns true when the placement indicates a pane should be shown.
func (p Placement) IsActive() bool {
	return p.Direction != ""
}
