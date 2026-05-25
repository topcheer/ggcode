package a2a

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
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

// Registry manages local A2A instance discovery via a shared JSON file.
type Registry struct {
	mu       sync.Mutex
	dir      string // ~/.ggcode/a2a/
	selfID   string
	selfInfo *InstanceInfo
	mdnsSvc  *mdnsService // nil if LAN discovery is disabled
}

// NewRegistry creates or opens the local instance registry.
func NewRegistry() (*Registry, error) {
	dir := filepath.Join(config.ConfigDir(), "a2a")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("a2a registry dir: %w", err)
	}
	return &Registry{dir: dir}, nil
}

// Register adds this instance to the registry.
// Cleans up stale files from the same PID, writes a new file,
// and optionally starts mDNS broadcasting.
func (r *Registry) Register(info InstanceInfo) error {
	r.mu.Lock()
	r.selfID = info.ID
	r.selfInfo = &info

	// Proactively remove all zombie files from dead processes.
	r.removeDeadPIDFiles()

	// Remove any stale files from a previous registration of this PID+workspace.
	r.removeStaleFiles(info.PID, info.Workspace, info.ID)

	err := r.writeInstanceFile(info)
	r.mu.Unlock()
	if err != nil {
		return err
	}

	// Start mDNS if LAN discovery is enabled.
	if r.mdnsSvc != nil {
		if startErr := r.mdnsSvc.start(info); startErr != nil {
			// mDNS failure is non-fatal — local discovery still works.
			debug.Log("a2a.registry", "mDNS registration warning: %v", startErr)
		}
	}
	return nil
}

// removeDeadPIDFiles removes all registry files belonging to dead processes.
// Called during Register() to proactively clean up zombie files that would
// otherwise accumulate until someone calls Discover().
func (r *Registry) removeDeadPIDFiles() {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(r.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var inst InstanceInfo
		if json.Unmarshal(data, &inst) == nil && inst.PID > 0 && !isPIDAlive(inst.PID) {
			os.Remove(path)
		}
	}
}

// removeStaleFiles deletes registry files from the same PID+workspace
// that aren't the current ID. This handles the case where a daemon
// restarts within the same PID and same workspace over time.
func (r *Registry) removeStaleFiles(pid int, workspace string, currentID string) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// Extract PID from filename: ggcode-hostname-PID-timestamp.json
		name := entry.Name()
		// The current file will be written after this cleanup, so skip it.
		if strings.Contains(name, currentID) {
			continue
		}
		// Read file to check PID match.
		path := filepath.Join(r.dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var inst InstanceInfo
		if json.Unmarshal(data, &inst) == nil && inst.PID == pid && inst.Workspace == workspace {
			os.Remove(path)
		}
	}
}

// Unregister removes this instance from the registry and stops mDNS.
func (r *Registry) Unregister() error {
	if r.mdnsSvc != nil {
		r.mdnsSvc.stop()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return os.Remove(r.instanceFilePath(r.selfID))
}

// Discover returns all running instances (excluding self).
// Merges local file discovery with mDNS LAN discovery, deduplicating by ID.
func (r *Registry) Discover() ([]InstanceInfo, error) {
	// 1) Local file discovery
	localInstances, err := r.discoverLocal()
	if err != nil {
		localInstances = nil // non-fatal
	}

	// 2) mDNS LAN discovery
	var mdnsInstances []InstanceInfo
	if r.mdnsSvc != nil {
		mdnsInstances = r.mdnsSvc.lookup()
	}

	// 3) Merge with dedup by ID, pruning dead PIDs from mDNS results
	seen := make(map[string]bool)
	var result []InstanceInfo
	for _, inst := range localInstances {
		if !seen[inst.ID] {
			seen[inst.ID] = true
			result = append(result, inst)
		}
	}
	for _, inst := range mdnsInstances {
		if seen[inst.ID] {
			continue
		}
		// mDNS may advertise instances whose avahi-publish is still running
		// but the actual ggcode process is dead (killed with SIGKILL).
		if inst.PID > 0 && !isPIDAlive(inst.PID) {
			continue
		}
		seen[inst.ID] = true
		result = append(result, inst)
	}
	return result, nil
}

// discoverLocal reads the local file-based registry, pruning dead PIDs
// and deduplicating stale files from the same PID+workspace combination.
func (r *Registry) discoverLocal() ([]InstanceInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	instances, err := r.loadAll()
	if err != nil {
		return nil, err
	}

	// Group by PID+workspace: keep only the latest (by StartedAt) per key,
	// remove stale files, and prune dead PIDs entirely.
	type pidWorkKey struct {
		pid       int
		workspace string
	}
	latest := make(map[pidWorkKey]InstanceInfo)
	files := make(map[pidWorkKey][]string) // key → list of file paths
	for _, inst := range instances {
		key := pidWorkKey{inst.PID, inst.Workspace}
		path := r.instanceFilePath(inst.ID)
		files[key] = append(files[key], path)

		existing, ok := latest[key]
		if !ok || inst.StartedAt > existing.StartedAt {
			latest[key] = inst
		}
	}

	var others []InstanceInfo
	for key, inst := range latest {
		if !isPIDAlive(key.pid) {
			// Dead PID — remove all its files.
			for _, p := range files[key] {
				os.Remove(p)
			}
			continue
		}
		// Remove stale files from the same key (older registrations).
		for _, p := range files[key] {
			if p != r.instanceFilePath(inst.ID) {
				os.Remove(p)
			}
		}
		if inst.ID != r.selfID {
			others = append(others, inst)
		}
	}
	return others, nil
}

// EnableLANDiscovery enables mDNS broadcasting for this registry.
// Must be called before Register.
func (r *Registry) EnableLANDiscovery() {
	r.mdnsSvc = newMDNSService()
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
		return r.writeInstanceFile(*r.selfInfo)
	}
	return nil
}

func (r *Registry) instancesDir() string {
	return r.dir
}

func (r *Registry) instanceFilePath(id string) string {
	return filepath.Join(r.dir, id+".json")
}

// loadAll reads all per-instance files from the registry directory.
func (r *Registry) loadAll() ([]InstanceInfo, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var instances []InstanceInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(r.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable files
		}
		var inst InstanceInfo
		if err := json.Unmarshal(data, &inst); err != nil {
			continue // skip corrupted files
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

func (r *Registry) writeInstanceFile(info InstanceInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.instanceFilePath(info.ID), data, 0644)
}

// isPIDAlive checks if a process with the given PID exists.
// Delegates to util.IsProcessAlive which uses syscall.Signal(0)
// for reliable detection across macOS and Linux.
func isPIDAlive(pid int) bool {
	return util.IsProcessAlive(pid)
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
	home := canonicalWorkspacePath(config.HomeDir())
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

// GenerateInstanceID creates a unique ID for this ggcode process.
func GenerateInstanceID() string {
	hostname, _ := os.Hostname()
	pid := os.Getpid()
	return fmt.Sprintf("ggcode-%s-%d-%d", hostname, pid, time.Now().UnixNano())
}
