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

const (
	maxBrowserProfiles = 4

	defaultBrowserProfileTTL = 7 * 24 * time.Hour

	browserProfileGCInterval = 6 * time.Hour
)

var validBrowserProfileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validateBrowserProfileName(name string) error {
	if !validBrowserProfileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use 1-64 chars of letters, digits, '.', '_' or '-' (no spaces, shell syntax like $(...), or path separators)", name)
	}
	return nil
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
		for _, tab := range c.p.tabs {
			tab.cancel()
		}
		c.p.tabs = nil
		c.p.allocCancel()
		delete(b.profiles, c.name)
		debug.Log("browser", "evicted LRU profile %q (over cap %d)", c.name, maxBrowserProfiles)
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
			ticker := time.NewTicker(browserProfileGCInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					b.gcStaleBrowserProfiles()
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
