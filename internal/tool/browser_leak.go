package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// Browser resource-leak guards.
//
// Incident: heavy browser-tool usage left 772 profile directories (104 GB)
// under ~/.ggcode/browser-profiles and hundreds of Chrome processes on the
// developer machine. Three leak surfaces, three guards:
//
//  1. Process accumulation: every distinct profile name spawns a Chrome
//     instance that lives until the agent exits gracefully. Agents love
//     inventing fresh profile names per task, so one session can hold a
//     dozen idle Chromes. maxBrowserProfiles evicts least-recently-used
//     profiles instead.
//  2. Disk accumulation: named profile dirs are never deleted, even when
//     their Chrome is long dead (SingletonLock points at a dead pid).
//     gcStaleBrowserProfiles removes dirs whose owner process is gone and
//     whose mtime exceeds the TTL.
//  3. Garbage names: "$(date +%H%M%S)" passed literally as a profile name
//     created directories with shell metacharacters. Profile names are now
//     validated.

const (
	// maxBrowserProfiles caps concurrent Chrome instances per Browser tool.
	// Each instance costs ~7-10 OS processes and 100-300 MB RSS.
	maxBrowserProfiles = 4

	// defaultBrowserProfileTTL is how long a dead profile directory may
	// linger before GC removes it.
	defaultBrowserProfileTTL = 7 * 24 * time.Hour

	// browserProfileGCInterval spaces GC sweeps.
	browserProfileGCInterval = 6 * time.Hour
)

// validBrowserProfileName matches safe, single-component profile names.
var validBrowserProfileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validateBrowserProfileName rejects empty, path-traversing, or shell-
// metacharacter names ("system"/"default" are handled by the caller).
func validateBrowserProfileName(name string) error {
	if !validBrowserProfileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use 1-64 chars of letters, digits, '.', '_' or '-' (no spaces, shell syntax like $(...), or path separators)", name)
	}
	return nil
}

// evictLRUBrowserProfiles closes least-recently-used profiles so at most
// maxBrowserProfiles remain. Callers hold b.mu.
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
	// Selection sort by lastUsed ascending; evict until within cap.
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
		for _, tab := range c.p.tabs {
			tab.cancel()
		}
		c.p.tabs = nil
		c.p.allocCancel()
		delete(b.profiles, c.name)
		debug.Log("browser", "evicted LRU profile %q (over cap %d)", c.name, maxBrowserProfiles)
	}
}

// browserProfilesRoot returns ~/.ggcode/browser-profiles.
func browserProfilesRoot() string {
	return filepath.Join(homeDir(), ".ggcode", "browser-profiles")
}

// singletonLockPID extracts the owning Chrome pid from a user-data-dir
// SingletonLock symlink target. Format on macOS/Linux: "<hostname>-<pid>".
// Returns 0 when absent or unparseable.
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

// gcStaleBrowserProfiles removes profile directories whose Chrome owner is
// dead and whose mtime exceeds the TTL. Live Chromes are detected via the
// SingletonLock pid and never touched; dirs without a lock are treated as
// dead. Live check also covers Chromes owned by other running ggcode
// processes. Disable with GGCODE_BROWSER_PROFILE_GC=0; tune TTL with
// GGCODE_BROWSER_PROFILE_TTL_DAYS.
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

	// Snapshot of profiles this process owns — never GC those.
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
		if time.Since(info.ModTime()) < ttl {
			continue
		}
		if pid := singletonLockPID(dir); pid != 0 && processAlive(pid) {
			continue // another live Chrome owns it
		}
		// #2091 review: the pre-loop `live` snapshot released b.mu before
		// iteration began, but getProfile adopts an existing on-disk dir by
		// name (its mtime stays TTL-aged until Chrome writes) - a profile
		// created mid-sweep would pass every check above while its Chrome
		// may not have written the SingletonLock yet, and RemoveAll would
		// delete the dir out from under the just-spawned Chrome. The sweep
		// iterates for a long time on incident-scale roots (hundreds of
		// dirs), making the window real. Re-validate under the lock right
		// before deleting; the residual window is the microseconds between
		// this check and RemoveAll.
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

// startBrowserProfileGC launches the background GC loop (once per Browser).
func (b *Browser) startBrowserProfileGC() {
	b.gcOnce.Do(func() {
		safego.Go("browser.profileGC", func() {
			b.gcStaleBrowserProfiles()
			ticker := time.NewTicker(browserProfileGCInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					b.gcStaleBrowserProfiles()
				case <-b.gcStopped():
					// #2096 bug A: Close() stops the loop so the goroutine
					// (and the Browser it references) does not outlive the
					// agent that owned it.
					return
				}
			}
		})
	})
}

// gcStopped returns the GC termination signal; a nil channel (direct
// &Browser{} construction) blocks forever, preserving the pre-#2096
// behavior for tests that never Close.
func (b *Browser) gcStopped() <-chan struct{} { return b.gcStop }

// touch updates the profile's LRU timestamp. Callers hold b.mu.
func (p *browserProfile) touch() {
	p.lastUsed = time.Now()
}
