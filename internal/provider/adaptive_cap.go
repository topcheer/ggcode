package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// adaptiveCap learns a working max-output-tokens value for a single
// (vendor, endpoint, model) triple by observing two server signals:
//
//   - finish_reason == length / max_tokens / MAX_TOKENS  → output truncated,
//     cap was too SMALL → ratchet up by capStep on the next call.
//   - HTTP 400 referencing max_tokens                    → server refused the
//     value, cap was too BIG → ratchet down by capStep (or to the parsed
//     server limit) on the next call.
//
// Invariants (guarantee no jitter / drift):
//   - lo only ever grows (highest cap that has been observed safe)
//   - hi only ever shrinks (lowest cap that has been observed rejected)
//   - cur always lies in [lo, hi] (clamped on every adjustment)
//   - once hi-lo <= capStep, further adjustments stop (converged)
//
// Persisted to ~/.ggcode/state/maxtokens.json across sessions.
type adaptiveCap struct {
	key string

	mu       sync.Mutex
	lo       int64 // highest known-safe cap (monotonic ↑)
	hi       int64 // lowest known-rejected cap (monotonic ↓), 0 means "unknown"
	cur      atomic.Int64
	userHint int64 // initial value from config; used for first-time bounds
}

const (
	capStep    = 4096
	capCeiling = 1 << 20 // sanity ceiling: 1M tokens
)

// Get returns the current cap to send on the next request. Always > 0 if the
// user configured a non-zero value; returns 0 to mean "don't send".
func (c *adaptiveCap) Get() int {
	if c == nil {
		return 0
	}
	return int(c.cur.Load())
}

// OnTruncated is called by a provider after observing a truncation finish
// reason. It bumps the cap upward (within [lo, hi]), persists the new state,
// and emits a debug log. Safe to call concurrently.
func (c *adaptiveCap) OnTruncated() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	cur := c.cur.Load()
	// `cur` is now known to be too small → it's a safe lower bound.
	if cur > c.lo {
		c.lo = cur
	}
	next := cur + capStep
	if c.hi > 0 && next >= c.hi {
		// Don't push up to the rejected ceiling; back off by 1.
		next = c.hi - 1
	}
	if next > capCeiling {
		next = capCeiling
	}
	if next <= cur {
		debug.Log("adaptive_cap", "%s TRUNCATED but converged: cur=%d lo=%d hi=%d (no change)", c.key, cur, c.lo, c.hi)
		return
	}
	c.cur.Store(next)
	debug.Log("adaptive_cap", "%s TRUNCATED: %d → %d (lo=%d hi=%d)", c.key, cur, next, c.lo, c.hi)
	saveAdaptiveCaps()
}

// OnRejected is called when the server returns an error indicating the
// configured max_tokens is too large. parsedLimit may be 0 if the server's
// upper bound could not be parsed from the error message; in that case the
// cap is simply backed off by capStep.
func (c *adaptiveCap) OnRejected(parsedLimit int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	cur := c.cur.Load()
	newHi := int64(parsedLimit)
	if newHi <= 0 {
		newHi = cur
	}
	if c.hi == 0 || newHi < c.hi {
		c.hi = newHi
	}
	// Step the cap down. Prefer the parsed server limit when known.
	var next int64
	if parsedLimit > 0 {
		next = int64(parsedLimit)
	} else {
		next = cur - capStep
	}
	if next < c.lo {
		next = c.lo
	}
	if next < 1 {
		next = 1
	}
	if next >= cur {
		next = cur - 1 // ensure progress
		if next < 1 {
			next = 1
		}
	}
	c.cur.Store(next)
	debug.Log("adaptive_cap", "%s REJECTED: %d → %d (lo=%d hi=%d, parsed=%d)", c.key, cur, next, c.lo, c.hi, parsedLimit)
	saveAdaptiveCaps()
}

// ─── registry / persistence ──────────────────────────────────────────────────

var (
	capRegistryMu sync.Mutex
	capRegistry   = map[string]*adaptiveCap{}
	capLoadOnce   sync.Once
)

// AdaptiveCapFor returns the singleton adaptiveCap for the given identity.
// Identity is keyed on (vendor, baseURL, model) to avoid mixing learned values
// across distinct endpoints that share a model name.
//
// userHint is the value the user (or default) configured. It is used as the
// initial `cur` only on first creation; subsequent calls return the existing
// learned cap (which has already been clamped by lo/hi from disk).
func AdaptiveCapFor(vendor, baseURL, model string, userHint int) *adaptiveCap {
	capLoadOnce.Do(loadAdaptiveCaps)

	key := capKey(vendor, baseURL, model)
	capRegistryMu.Lock()
	defer capRegistryMu.Unlock()

	if c, ok := capRegistry[key]; ok {
		return c
	}
	c := &adaptiveCap{
		key:      key,
		userHint: int64(userHint),
	}
	if persisted, ok := persistedCaps[key]; ok {
		c.lo = persisted.Lo
		c.hi = persisted.Hi
		// Start from the persisted value but allow the user's new hint to
		// move us within the known-safe range.
		start := persisted.Cur
		hint := int64(userHint)
		if hint > 0 {
			if c.hi > 0 && hint >= c.hi {
				start = c.hi - 1
			} else if hint > c.lo {
				start = hint
			} else {
				start = c.lo
			}
		}
		if start < 1 {
			start = 1
		}
		c.cur.Store(start)
	} else {
		c.cur.Store(int64(userHint))
	}
	capRegistry[key] = c
	return c
}

func capKey(vendor, baseURL, model string) string {
	return strings.Join([]string{
		strings.TrimSpace(vendor),
		strings.TrimSpace(baseURL),
		strings.TrimSpace(model),
	}, "|")
}

type persistedCap struct {
	Lo  int64 `json:"lo"`
	Hi  int64 `json:"hi"`
	Cur int64 `json:"cur"`
}

var persistedCaps = map[string]persistedCap{}

func adaptiveCapsPath() string {
	return filepath.Join(config.ConfigDir(), "state", "maxtokens.json")
}

func loadAdaptiveCaps() {
	path := adaptiveCapsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	m := map[string]persistedCap{}
	if err := json.Unmarshal(data, &m); err != nil {
		debug.Log("adaptive_cap", "ignoring corrupt %s: %v", path, err)
		return
	}
	persistedCaps = m
}

// saveAdaptiveCaps snapshots the live registry (lo/hi/cur) and atomically
// writes it. Caller may hold an individual cap's mu; we briefly take the
// registry mu to enumerate.
func saveAdaptiveCaps() {
	capRegistryMu.Lock()
	snap := make(map[string]persistedCap, len(capRegistry))
	for k, c := range capRegistry {
		// Read fields without taking c.mu — we're already inside the caller's
		// lock for the cap being mutated, and other caps' fields are read
		// atomically (cur) or are stable enough for a snapshot.
		snap[k] = persistedCap{Lo: c.lo, Hi: c.hi, Cur: c.cur.Load()}
	}
	capRegistryMu.Unlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	path := adaptiveCapsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		debug.Log("adaptive_cap", "mkdir failed: %v", err)
		return
	}
	if err := util.AtomicWriteFile(path, data, 0o644); err != nil {
		debug.Log("adaptive_cap", "save failed: %v", err)
	}
}

// ─── error parsing ───────────────────────────────────────────────────────────

// maxTokensRejection inspects an error returned by a provider's HTTP/SDK call
// and reports whether it indicates the max_tokens parameter exceeded the
// model's allowed range. parsedLimit is non-zero if the upstream message
// contained an explicit ceiling like "must be <= 8192".
func maxTokensRejection(err error) (rejected bool, parsedLimit int) {
	if err == nil {
		return false, 0
	}
	msg := strings.ToLower(err.Error())
	// Don't false-positive on context-window errors — those are about input,
	// not output cap.
	if strings.Contains(msg, "context window") || strings.Contains(msg, "context length") {
		return false, 0
	}
	hasMaxTok := strings.Contains(msg, "max_tokens") ||
		strings.Contains(msg, "max tokens") ||
		strings.Contains(msg, "maxoutputtokens") ||
		strings.Contains(msg, "max_completion_tokens") ||
		strings.Contains(msg, "max output tokens")
	if !hasMaxTok {
		return false, 0
	}
	// Only treat as "too large"; ignore "must be > 0" style messages.
	tooLarge := strings.Contains(msg, "too large") ||
		strings.Contains(msg, "exceed") ||
		strings.Contains(msg, "less than") ||
		strings.Contains(msg, "<=") ||
		strings.Contains(msg, "at most") ||
		strings.Contains(msg, "must be smaller") ||
		strings.Contains(msg, "maximum")
	if !tooLarge {
		return false, 0
	}
	return true, parseLimitFromMessage(msg)
}

var limitNumberRe = regexp.MustCompile(`(?:<=|≤|less than or equal to|at most|maximum (?:of |is |allowed )?)\s*([0-9][0-9_,]{2,})`)

func parseLimitFromMessage(msg string) int {
	m := limitNumberRe.FindStringSubmatch(msg)
	if len(m) < 2 {
		return 0
	}
	cleaned := strings.NewReplacer(",", "", "_", "").Replace(m[1])
	n, err := strconv.Atoi(cleaned)
	if err != nil || n <= 0 || n > capCeiling {
		return 0
	}
	return n
}

// String is a debug helper.
func (c *adaptiveCap) String() string {
	if c == nil {
		return "<nil>"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("%s(cur=%d lo=%d hi=%d)", c.key, c.cur.Load(), c.lo, c.hi)
}
