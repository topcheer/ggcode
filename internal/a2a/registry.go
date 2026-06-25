package a2a

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// InstanceInfo describes a running ggcode instance for discovery.
type InstanceInfo struct {
	ID           string `json:"id"`
	PID          int    `json:"pid"`
	Workspace    string `json:"workspace"`
	StartedAt    string `json:"started_at"`
	Endpoint     string `json:"endpoint"`
	AgentCardURL string `json:"agent_card_url"`
	Status       string `json:"status"` // "ready", "busy", "stopping"
}

// DisplayName returns a human-readable identifier: "workspace-name:port".
// Two instances in the same directory are distinguished by port.
func (i InstanceInfo) DisplayName() string {
	name := filepath.Base(i.Workspace)
	_, port, err := net.SplitHostPort(i.Endpoint)
	if err != nil {
		return name
	}
	return name + ":" + port
}

// Registry manages A2A instance discovery via mDNS.
type Registry struct {
	mu       sync.Mutex
	selfID   string
	selfInfo *InstanceInfo
	mdnsSvc  *mdnsService // nil if LAN discovery is disabled

	// interfaces controls which NICs mDNS advertises on. Set via
	// SetInterfaces before Register. If nil, default-route auto-detection
	// runs inside mdns.start().
	interfaces []string

	// asyncCache is populated by a background goroutine so the UI thread
	// never blocks on mDNS lookups.
	asyncCache   []InstanceInfo
	asyncCacheOK bool
}

// backgroundRefreshInterval is how often the background goroutine refreshes.
const backgroundRefreshInterval = 15 * time.Second

// NewRegistry creates a new registry with mDNS discovery always enabled.
func NewRegistry() (*Registry, error) {
	return &Registry{
		mdnsSvc: newMDNSService(),
	}, nil
}

// SetInterfaces configures which network interfaces mDNS should advertise on.
// Must be called before Register. Pass nil to auto-detect the default route.
func (r *Registry) SetInterfaces(ifaces []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.interfaces = ifaces
}

// Register records self info and starts mDNS broadcasting.
func (r *Registry) Register(info InstanceInfo) error {
	r.mu.Lock()
	r.selfID = info.ID
	r.selfInfo = &info
	r.asyncCacheOK = false
	ifaces := r.interfaces
	r.mu.Unlock()

	if r.mdnsSvc != nil {
		if startErr := r.mdnsSvc.start(info, ifaces); startErr != nil {
			debug.Log("a2a.registry", "mDNS registration warning: %v", startErr)
		}
	}
	return nil
}

// SelfID returns this instance's ID.
func (r *Registry) SelfID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selfID
}

// Unregister stops mDNS broadcasting.
func (r *Registry) Unregister() error {
	if r.mdnsSvc != nil {
		r.mdnsSvc.stop()
	}
	return nil
}

// Discover returns all running instances found via mDNS (excluding self).
// When mDNS is not enabled, returns the cached instance list (useful for
// testing and single-machine scenarios).
func (r *Registry) Discover() ([]InstanceInfo, error) {
	var instances []InstanceInfo
	if r.mdnsSvc != nil {
		instances = r.mdnsSvc.lookup()
	} else {
		// No mDNS — use cached instances (populated by tests or manual injection).
		instances = r.CachedInstances()
	}

	// Update cache.
	r.mu.Lock()
	r.asyncCache = instances
	r.asyncCacheOK = true
	r.mu.Unlock()

	return instances, nil
}

// CachedInstances returns the last background-refreshed instance list without
// any mDNS I/O. Returns nil if the background refresh hasn't populated the
// cache yet. Safe to call from the UI thread.
func (r *Registry) CachedInstances() []InstanceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.asyncCacheOK {
		return nil
	}
	return append([]InstanceInfo(nil), r.asyncCache...)
}

// StartBackgroundRefresh launches a goroutine that periodically calls
// Discover() to keep the async cache fresh.
func (r *Registry) StartBackgroundRefresh(ctx context.Context) {
	safego.Go("a2a.registry.backgroundRefresh", func() {
		// Initial quick refreshes so cache populates fast (mDNS browser
		// exponential backoff starts at ~1s, so retry a few times).
		for i := 0; i < 5; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			r.refreshCache()
		}
		ticker := time.NewTicker(backgroundRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.refreshCache()
			}
		}
	})
}

func (r *Registry) refreshCache() {
	instances, err := r.Discover()
	if err != nil {
		debug.Log("a2a.registry", "background refresh error: %v", err)
		return
	}
	r.mu.Lock()
	r.asyncCache = instances
	r.asyncCacheOK = true
	r.mu.Unlock()
}

// InvalidateDiscoverCache forces the next background refresh to do a fresh scan.
func (r *Registry) InvalidateDiscoverCache() {
	r.mu.Lock()
	r.asyncCacheOK = false
	r.mu.Unlock()
}

// SetCachedInstances manually sets the in-memory instance cache. This is
// used by tests to inject instances without requiring real mDNS discovery.
func (r *Registry) SetCachedInstances(instances []InstanceInfo) {
	r.mu.Lock()
	r.asyncCache = append([]InstanceInfo(nil), instances...)
	r.asyncCacheOK = true
	r.mu.Unlock()
}

// DiscoverByCapability returns instances whose metadata matches the given tag.
// Tag can be a language name ("go", "typescript"), framework ("npm"), or partial workspace path.
func (r *Registry) DiscoverByCapability(tag string) ([]InstanceInfo, error) {
	all, err := r.Discover()
	if err != nil {
		return nil, err
	}

	tag = strings.ToLower(tag)
	var matched []InstanceInfo
	for _, inst := range all {
		ws := strings.ToLower(inst.Workspace)
		if strings.Contains(ws, tag) || strings.Contains(tag, filepath.Base(ws)) {
			matched = append(matched, inst)
		}
	}
	return matched, nil
}

// UpdateStatus refreshes the status field for this instance.
func (r *Registry) UpdateStatus(status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.selfInfo != nil {
		r.selfInfo.Status = status
		// mDNS TXT records are set at registration time; future enhancement
		// could update them dynamically. For now we just update the in-memory copy.
	}
	return nil
}

// GenerateInstanceID creates a unique instance ID.
func GenerateInstanceID() string {
	hostname, _ := os.Hostname()
	pid := os.Getpid()
	return fmt.Sprintf("ggcode-%s-%d-%d", hostname, pid, time.Now().UnixNano())
}

// SelfInfo returns the InstanceInfo for this instance (safe copy).
func (r *Registry) SelfInfo() *InstanceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.selfInfo == nil {
		return nil
	}
	info := *r.selfInfo
	return &info
}

// ListAllInstances returns all known instances sorted by workspace name.
func (r *Registry) ListAllInstances() ([]InstanceInfo, error) {
	instances, err := r.Discover()
	if err != nil {
		return nil, err
	}
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].Workspace < instances[j].Workspace
	})

	// Prepend self if registered.
	r.mu.Lock()
	if r.selfInfo != nil {
		instances = append([]InstanceInfo{*r.selfInfo}, instances...)
	}
	r.mu.Unlock()

	return instances, nil
}

// ---------------------------------------------------------------------------
// Workspace detection
// ---------------------------------------------------------------------------

func detectWorkspaceMeta(dir string) WorkspaceMeta {
	meta := WorkspaceMeta{
		Workspace: dir,
		ProjName:  filepath.Base(dir),
		HasGit:    false,
	}

	// Check for git.
	gitDir := filepath.Join(dir, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		meta.HasGit = true
	}

	// Detect frameworks.
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "go-modules")
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "npm")
	}
	if _, err := os.Stat(filepath.Join(dir, "requirements.txt")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "pip")
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "cargo")
	}

	if shouldSkipRecursiveWorkspaceMeta(dir) {
		return meta
	}

	meta.Languages, meta.HasTests = scanWorkspaceSignals(dir)

	return meta
}

const (
	workspaceMetaMaxScanDepth = 3
	workspaceMetaMaxEntries   = 5000
)

func scanWorkspaceSignals(dir string) ([]string, bool) {
	rootDepth := strings.Count(filepath.Clean(dir), string(filepath.Separator))
	langSet := make(map[string]bool)
	hasTests := false
	visited := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		visited++
		if visited > workspaceMetaMaxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			base := strings.ToLower(d.Name())
			if shouldSkipWorkspaceMetaDir(base) {
				return filepath.SkipDir
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > workspaceMetaMaxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(d.Name())
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go":
			langSet["go"] = true
		case ".ts", ".tsx":
			langSet["typescript"] = true
		case ".js", ".jsx":
			langSet["javascript"] = true
		case ".py":
			langSet["python"] = true
		case ".rs":
			langSet["rust"] = true
		case ".java":
			langSet["java"] = true
		}
		if strings.Contains(name, "_test.") || strings.Contains(name, "test.") || strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
			hasTests = true
		}
		return nil
	})
	langs := make([]string, 0, len(langSet))
	for lang := range langSet {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	return langs, hasTests
}

func shouldSkipRecursiveWorkspaceMeta(dir string) bool {
	absDir := canonicalWorkspacePath(dir)
	home := canonicalWorkspacePath(homeDir())
	return absDir == string(filepath.Separator) || (home != "" && strings.EqualFold(absDir, home))
}

func canonicalWorkspacePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func shouldSkipWorkspaceMetaDir(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "node_modules", "dist", "build", "target", "coverage", "tmp", "temp", "cache", "logs", "bin", "obj", "__pycache__", "venv":
		return true
	default:
		return false
	}
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
