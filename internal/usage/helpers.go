package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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

// flexFloat accepts both JSON numbers and strings for the same field.
// DeepSeek's /user/balance returns total_balance as a QUOTED string
// ("110.00") while other vendors send bare numbers - a plain float64
// field made the whole probe fail with "json: cannot unmarshal string
// into Go struct" (user report 2026-09-19). 2026-09-20: also applied to
// Moonshot's available_balance which shares the string form.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if s[0] == '"' {
		s = strings.Trim(s, "\"")
		if s == "" {
			*f = 0
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("flexFloat: %w", err)
	}
	*f = flexFloat(v)
	return nil
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
