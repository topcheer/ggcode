package usage

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// clampPercent guards vendor payloads that report slightly out-of-range
// values (99.999 -> 100).
func clampPercent(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// parseResetTime tolerates empty and RFC3339 strings.
func parseResetTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// decodeJSON decodes from a live response (adapter-local, keeps
// adapters.go' getJSON untouched for the beta-header variant).
func decodeJSON(resp *http.Response, out any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}
