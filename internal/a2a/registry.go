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

type InstanceInfo struct {
	ID           string `json:"id"`
	PID          int    `json:"pid"`
	Workspace    string `json:"workspace"`
	StartedAt    string `json:"started_at"`
	Endpoint     string `json:"endpoint"`
	AgentCardURL string `json:"agent_card_url"`
	Status       string `json:"status"`
	// Seat identity (#5616): lanchat-derived nick/role/team so a2a target
	// resolution can address a concrete seat inside a same-workspace fleet
	// where every instance registers under the same project name. Empty for
	// peers that never joined lanchat or for pre-#5616 roster/mDNS entries.
	Nick     string `json:"nick,omitempty"`
	Role     string `json:"role,omitempty"`
	Team     string `json:"team,omitempty"`
	LastSeen int64  `json:"last_seen,omitempty"` // unix seconds; 0 = unknown
}

func (i InstanceInfo) DisplayName() string {
	name := filepath.Base(i.Workspace)
	_, port, err := net.SplitHostPort(i.Endpoint)
	if err != nil {
		return name
	}
	return name + ":" + port
}

type Registry struct {
	mu       sync.Mutex
	selfID   string
	selfInfo *InstanceInfo
	mdnsSvc  *mdnsService

	interfaces []string

	asyncCache   []InstanceInfo
	asyncCacheOK bool
}

const backgroundRefreshInterval = 15 * time.Second

func NewRegistry() (*Registry, error) {
	return &Registry{
		mdnsSvc: newMDNSService(),
	}, nil
}

func (r *Registry) SetInterfaces(ifaces []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.interfaces = ifaces
}

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
	if info.Workspace != "" {
		RosterUpsert(info.Workspace, info)
	}
	return nil
}

// SelfInfo returns a copy of the registered self instance (nil before Register).
func (r *Registry) SelfInfo() *InstanceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.selfInfo == nil {
		return nil
	}
	c := *r.selfInfo
	return &c
}

// SetSelfIdentity stamps the lanchat seat identity (nick/role/team) onto
// the registered self and republishes the roster entry so cross-process
// resolvers (one-shot CLI with no lanchat hub) can address this seat by
// nick (#5616). Call after Register, once the hub exists.
func (r *Registry) SetSelfIdentity(nick, role, team string) {
	r.mu.Lock()
	if r.selfInfo == nil {
		r.mu.Unlock()
		return
	}
	if nick != "" {
		r.selfInfo.Nick = nick
	}
	if role != "" {
		r.selfInfo.Role = role
	}
	if team != "" {
		r.selfInfo.Team = team
	}
	info := *r.selfInfo
	ws := info.Workspace
	r.mu.Unlock()
	if ws != "" {
		RosterUpsert(ws, info)
	}
}

func (r *Registry) SelfID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selfID
}

// SelfWorkspace returns the registered self's workspace ("" before Register).
func (r *Registry) SelfWorkspace() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.selfInfo == nil {
		return ""
	}
	return r.selfInfo.Workspace
}

func (r *Registry) Unregister() error {
	if r.mdnsSvc != nil {
		r.mdnsSvc.stop()
	}
	r.mu.Lock()
	id := r.selfID
	ws := ""
	if r.selfInfo != nil {
		ws = r.selfInfo.Workspace
	}
	r.mu.Unlock()
	if ws != "" {
		RosterRemove(ws, id)
	}
	return nil
}

func (r *Registry) Discover() ([]InstanceInfo, error) {
	var instances []InstanceInfo
	if r.mdnsSvc != nil {
		instances = r.mdnsSvc.lookup()
	} else {
		instances = r.CachedInstances()
	}

	r.mu.Lock()
	r.asyncCache = append([]InstanceInfo(nil), instances...)
	r.asyncCacheOK = true
	r.mu.Unlock()

	return append([]InstanceInfo(nil), instances...), nil
}

func (r *Registry) CachedInstances() []InstanceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.asyncCacheOK {
		return nil
	}
	return append([]InstanceInfo(nil), r.asyncCache...)
}

func (r *Registry) StartBackgroundRefresh(ctx context.Context) {
	safego.Go("a2a.registry.backgroundRefresh", func() {
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

func (r *Registry) SetCachedInstances(instances []InstanceInfo) {
	r.mu.Lock()
	r.asyncCache = append([]InstanceInfo(nil), instances...)
	r.asyncCacheOK = true
	r.mu.Unlock()
}

func (r *Registry) UpdateStatus(status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.selfInfo != nil {
		r.selfInfo.Status = status
	}
	return nil
}

func GenerateInstanceID() string {
	hostname, _ := os.Hostname()
	pid := os.Getpid()
	return fmt.Sprintf("ggcode-%s-%d-%d", hostname, pid, time.Now().UnixNano())
}

func detectWorkspaceMeta(dir string) WorkspaceMeta {
	meta := WorkspaceMeta{
		Workspace: dir,
		ProjName:  filepath.Base(dir),
		HasGit:    false,
	}

	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		meta.HasGit = true
	}

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
		if strings.Contains(name, "_test.") || strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
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
