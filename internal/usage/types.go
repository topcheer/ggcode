// Package usage implements the first-class usage/balance surface (#2150):
// per-vendor probes for pay-as-you-go balance and coding-plan rolling
// windows, with a caching service so callers (TUI sidebar, /usage command,
// IM, desktop) share one query pipeline.
package usage

import (
	"context"
	"time"
)

// UsageWindow is one rolling usage window (e.g. a 5h coding-plan window
// or a weekly quota).
type UsageWindow struct {
	Label       string    // "5h" / "weekly"
	UsedPercent float64   // 0..100
	ResetsAt    time.Time // zero when unknown
}

// UsageInfo is the normalized result of one vendor probe.
type UsageInfo struct {
	Vendor  string
	Balance *float64 // PAYG balance, nil when the vendor has no balance concept
	Windows []UsageWindow
	Source  string // probe endpoint short name, for diagnostics
}

// Probe fetches usage for one vendor. Implementations must be safe for
// concurrent use. apiKey/baseURL come from the resolved active vendor
// config; the context bounds the HTTP call (service applies a timeout).
type Probe interface {
	Vendor() string
	Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error)
}
