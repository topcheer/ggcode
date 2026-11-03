package usage

import (
	"context"
	"time"
)

type UsageWindow struct {
	Label       string
	UsedPercent float64
	ResetsAt    time.Time
}

type UsageInfo struct {
	Vendor  string
	Balance *float64
	Windows []UsageWindow
	Source  string
}

type URLMatcher interface {
	MatchesURL(baseURL string) bool
}

type Probe interface {
	Vendor() string
	Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error)
}
