package usage

import (
	"fmt"
	"strings"
	"time"
)

// RenderText renders one probe result as the short multi-line text used by
// the IM /usage path (and available to any other text surface). Errors
// degrade to a single explanatory line - usage is ambient information.
func RenderText(vendor string, info *UsageInfo, err error) string {
	if err != nil {
		return fmt.Sprintf("usage: %s (%s)", vendor, err.Error())
	}
	if info == nil {
		return fmt.Sprintf("usage: %s (no data)", vendor)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "usage %s", info.Vendor)
	if info.Balance != nil {
		fmt.Fprintf(&b, " | balance %.2f", *info.Balance)
	}
	for _, w := range info.Windows {
		fmt.Fprintf(&b, "\n  %s window: %.0f%% used", w.Label, w.UsedPercent)
		if !w.ResetsAt.IsZero() {
			fmt.Fprintf(&b, " (resets %s)", w.ResetsAt.Format(time.RFC3339))
		}
	}
	return b.String()
}
