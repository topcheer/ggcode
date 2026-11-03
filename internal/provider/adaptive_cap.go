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

type adaptiveCap struct {
	key string

	mu       sync.Mutex
	lo       int64
	hi       int64
	cur      atomic.Int64
	userHint int64
}

const (
	capStep    = 4096
	capCeiling = 1 << 20
)

func (c *adaptiveCap) Get() int {
	if c == nil {
		return 0
	}
	return int(c.cur.Load())
}

func (c *adaptiveCap) OnTruncated() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cur := c.cur.Load()

	if cur > c.lo {
		c.lo = cur
	}
	next := cur + capStep
	if c.hi > 0 && next >= c.hi {

		next = c.hi - 1
	}
	if next > capCeiling {
		next = capCeiling
	}
	if next <= cur {
		c.mu.Unlock()
		debug.Log("adaptive_cap", "%s TRUNCATED but converged: cur=%d lo=%d hi=%d (no change)", c.key, cur, c.lo, c.hi)
		return
	}
	c.cur.Store(next)
	debug.Log("adaptive_cap", "%s TRUNCATED: %d → %d (lo=%d hi=%d)", c.key, cur, next, c.lo, c.hi)
	c.mu.Unlock()
	saveAdaptiveCaps()
}

func (c *adaptiveCap) OnRejected(parsedLimit int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	cur := c.cur.Load()
	newHi := int64(parsedLimit)
	if newHi <= 0 {
		newHi = cur
	}
	if c.hi == 0 || newHi < c.hi {
		if newHi < c.lo {
			newHi = c.lo
		}
		c.hi = newHi
	}
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

		next = cur
	}
	c.cur.Store(next)
	debug.Log("adaptive_cap", "%s REJECTED: %d → %d (lo=%d hi=%d, parsed=%d)", c.key, cur, next, c.lo, c.hi, parsedLimit)
	c.mu.Unlock()
	saveAdaptiveCaps()
}

var (
	capRegistryMu sync.Mutex
	capRegistry   = map[string]*adaptiveCap{}
	capLoadOnce   sync.Once
)

func AdaptiveCapFor(vendor, baseURL, model string, userHint int) *adaptiveCap {
	capLoadOnce.Do(loadAdaptiveCaps)

	key := capKey(vendor, baseURL, model)
	capRegistryMu.Lock()
	defer capRegistryMu.Unlock()

	if c, ok := capRegistry[key]; ok {

		if hint := int64(userHint); hint > 0 && hint < c.cur.Load() {
			c.userHint = hint
			c.cur.Store(hint)
		}
		return c
	}
	c := &adaptiveCap{
		key:      key,
		userHint: int64(userHint),
	}
	if persisted, ok := persistedCaps[key]; ok {
		c.lo = persisted.Lo
		c.hi = persisted.Hi

		start := persisted.Cur
		hint := int64(userHint)
		if hint > 0 {
			switch {
			case c.hi > 0 && hint >= c.hi:
				start = c.hi - 1
				if start < c.lo {

					start = c.lo
				}
			case hint > c.lo:
				start = hint
			default:

				start = hint
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

func AdaptiveCapForModelSwap(old *adaptiveCap, newModel string, userHint int) *adaptiveCap {
	if old == nil {
		return nil
	}

	vendor := old.key
	baseURL := ""
	if i := strings.Index(old.key, "|"); i >= 0 {
		vendor = old.key[:i]
		rest := old.key[i+1:]
		if j := strings.LastIndex(rest, "|"); j >= 0 {
			baseURL = rest[:j]
		}
	}
	return AdaptiveCapFor(vendor, baseURL, newModel, userHint)
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

func saveAdaptiveCaps() {

	capRegistryMu.Lock()
	keys := make([]string, 0, len(capRegistry))
	caps := make([]*adaptiveCap, 0, len(capRegistry))
	for k, c := range capRegistry {
		keys = append(keys, k)
		caps = append(caps, c)
	}
	capRegistryMu.Unlock()

	snap := make(map[string]persistedCap, len(caps))
	for i, c := range caps {
		c.mu.Lock()
		snap[keys[i]] = persistedCap{Lo: c.lo, Hi: c.hi, Cur: c.cur.Load()}
		c.mu.Unlock()
	}

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

func maxTokensRejection(err error) (rejected bool, parsedLimit int) {
	if err == nil {
		return false, 0
	}

	if IsContextOverflowError(err) {
		return false, 0
	}
	msg := strings.ToLower(err.Error())
	hasMaxTok := strings.Contains(msg, "max_tokens") ||
		strings.Contains(msg, "max tokens") ||
		strings.Contains(msg, "maxoutputtokens") ||
		strings.Contains(msg, "max_completion_tokens") ||
		strings.Contains(msg, "max output tokens")
	if !hasMaxTok {
		return false, 0
	}

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

func (c *adaptiveCap) String() string {
	if c == nil {
		return "<nil>"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("%s(cur=%d lo=%d hi=%d)", c.key, c.cur.Load(), c.lo, c.hi)
}
