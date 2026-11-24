package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	chromedp "github.com/chromedp/chromedp"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	maxBrowserProfiles = 4

	defaultBrowserProfileTTL = 7 * 24 * time.Hour

	browserProfileGCInterval = 6 * time.Hour

	// defaultBrowserIdleTimeout bounds how long an unused browser instance
	// outlives its last tool call. Agents almost never call action=close, so
	// without this the Chrome process (and, on an animated / 3D page, its
	// SwiftShader GPU helper at 100%+ CPU per core) stays alive until the LRU
	// cap or process exit - hours to days on a busy machine.
	defaultBrowserIdleTimeout = 3 * time.Minute

	browserIdleCheckInterval = 30 * time.Second
)

// browserIdleTimeout returns the idle threshold; GGCODE_BROWSER_IDLE_SECS
// overrides it (0 disables idle reaping).
func browserIdleTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv("GGCODE_BROWSER_IDLE_SECS"))
	if v == "" {
		return defaultBrowserIdleTimeout
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return defaultBrowserIdleTimeout
	}
	return time.Duration(secs) * time.Second
}

var validBrowserProfileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validateBrowserProfileName(name string) error {
	if !validBrowserProfileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use 1-64 chars of letters, digits, '.', '_' or '-' (no spaces, shell syntax like $(...), or path separators)", name)
	}
	return nil
}

// closeProfileLocked tears a profile down: every tab context is cancelled
// (closing the first tab shuts the browser down in chromedp) and the
// allocator context is cancelled, which kills the Chrome process. Caller
// holds b.mu.
func (b *Browser) closeProfileLocked(name string, p *browserProfile) {
	for _, tab := range p.tabs {
		tab.cancel()
	}
	p.tabs = nil
	p.allocCancel()
	delete(b.profiles, name)
}

// reapIdleBrowserProfiles shuts down every profile whose last use is older
// than the idle timeout. It is the only thing that ends a browser the agent
// forgot to close; LRU eviction alone needs a 5th profile to fire.
func (b *Browser) reapIdleBrowserProfiles(now time.Time) {
	idle := browserIdleTimeout()
	if idle <= 0 {
		return
	}
	type cand struct {
		name string
		p    *browserProfile
	}
	b.mu.Lock()
	var stale []cand
	for name, p := range b.profiles {
		if now.Sub(p.lastUsed) > idle {
			stale = append(stale, cand{name, p})
		}
	}
	for _, c := range stale {
		idleFor := now.Sub(c.p.lastUsed).Round(time.Second)
		b.closeProfileLocked(c.name, c.p)
		debug.Log("browser", "reaped idle profile %q (unused for %s) - Chrome process shut down", c.name, idleFor)
	}
	b.mu.Unlock()
}

func (b *Browser) evictLRUBrowserProfiles() {
	if len(b.profiles) <= maxBrowserProfiles {
		return
	}
	type cand struct {
		name string
		p    *browserProfile
	}
	all := make([]cand, 0, len(b.profiles))
	for name, p := range b.profiles {
		all = append(all, cand{name, p})
	}

	for i := 0; i < len(all); i++ {
		minIdx := i
		for j := i + 1; j < len(all); j++ {
			if all[j].p.lastUsed.Before(all[minIdx].p.lastUsed) {
				minIdx = j
			}
		}
		all[i], all[minIdx] = all[minIdx], all[i]
	}
	for _, c := range all[:len(all)-maxBrowserProfiles] {
		idleFor := time.Since(c.p.lastUsed).Round(time.Second)
		b.closeProfileLocked(c.name, c.p)
		debug.Log("browser", "evicted LRU profile %q (over cap %d, unused for %s)", c.name, maxBrowserProfiles, idleFor)
	}
}

func browserProfilesRoot() string {
	return filepath.Join(homeDir(), ".ggcode", "browser-profiles")
}

func singletonLockPID(profileDir string) int {
	target, err := os.Readlink(filepath.Join(profileDir, "SingletonLock"))
	if err != nil {
		return 0
	}
	base := filepath.Base(strings.TrimSpace(target))
	idx := strings.LastIndexByte(base, '-')
	if idx < 0 || idx == len(base)-1 {
		return 0
	}
	pid, err := strconv.Atoi(base[idx+1:])
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func lockfilePath(profileDir string) string {
	return filepath.Join(profileDir, "lockfile")
}

func (b *Browser) gcStaleBrowserProfiles() {
	if os.Getenv("GGCODE_BROWSER_PROFILE_GC") == "0" {
		return
	}
	ttl := defaultBrowserProfileTTL
	if v := os.Getenv("GGCODE_BROWSER_PROFILE_TTL_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days >= 0 {
			ttl = time.Duration(days) * 24 * time.Hour
		}
	}
	root := browserProfilesRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	b.mu.Lock()
	live := make(map[string]bool, len(b.profiles))
	for name := range b.profiles {
		live[name] = true
	}
	b.mu.Unlock()

	var removed int
	for _, e := range entries {
		if !e.IsDir() || live[e.Name()] {
			continue
		}
		dir := filepath.Join(root, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.Name() == "system" {

			continue
		}
		if time.Since(info.ModTime()) < ttl {
			continue
		}
		if pid := singletonLockPID(dir); pid != 0 && processAlive(pid) {
			continue
		}

		if windowsProfileLockHeld(dir) {
			continue
		}

		b.mu.Lock()
		_, adopted := b.profiles[e.Name()]
		b.mu.Unlock()
		if adopted {
			continue
		}
		if err := os.RemoveAll(dir); err == nil {
			removed++
			debug.Log("browser", "GC removed stale profile dir %s (owner dead, age %s)", dir, time.Since(info.ModTime()).Round(time.Hour))
		}
	}
	if removed > 0 {
		debug.Log("browser", "GC removed %d stale browser profile dirs under %s", removed, root)
	}
}

func (b *Browser) startBrowserProfileGC() {
	b.gcOnce.Do(func() {
		safego.Go("browser.profileGC", func() {
			b.gcStaleBrowserProfiles()
			staleTicker := time.NewTicker(browserProfileGCInterval)
			defer staleTicker.Stop()
			idleTicker := time.NewTicker(browserIdleCheckInterval)
			defer idleTicker.Stop()
			for {
				select {
				case <-staleTicker.C:
					b.gcStaleBrowserProfiles()
				case <-idleTicker.C:
					b.reapIdleBrowserProfiles(time.Now())
				case <-b.gcStopped():

					return
				}
			}
		})
	})
}

func (b *Browser) gcStopped() <-chan struct{} { return b.gcStop }

func (p *browserProfile) touch() {
	p.lastUsed = time.Now()
}

// touchProfile marks a profile as used at the end of an action. Unknown
// names (action failed before the profile existed) are ignored.
func (b *Browser) touchProfile(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.profiles[name]; ok {
		p.touch()
	}
}

// setProfileLifecycle best-effort freezes ("frozen") or resumes ("active")
// every tab of a profile. Only headless profiles are frozen: a visible window
// is there for a human to interact with (e.g. logging in by hand), and a
// frozen page ignores input. Freezing stops timers, rAF and animations, which
// is what turns an idle-but-alive headless browser's GPU load to zero.
func (b *Browser) setProfileLifecycle(name string, state page.SetWebLifecycleStateState) {
	b.mu.Lock()
	p, ok := b.profiles[name]
	var tabs []*browserTab
	if ok && p.headless {
		for _, t := range p.tabs {
			tabs = append(tabs, t)
		}
	}
	b.mu.Unlock()
	for _, tab := range tabs {
		if tab.ctx.Err() != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(tab.ctx, 3*time.Second)
		_, _ = cdp.Call(ctx, chromedp.FromContext(ctx).Target, page.SetWebLifecycleState, page.SetWebLifecycleStateParams{State: state})
		cancel()
	}
}
