package a2a

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
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

// Registry manages local A2A instance discovery via a shared JSON file.
type Registry struct {
	mu       sync.Mutex
	dir      string // ~/.ggcode/a2a/
	selfID   string
	selfInfo *InstanceInfo
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
func (r *Registry) Register(info InstanceInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.selfID = info.ID
	r.selfInfo = &info

	instances, err := r.load()
	if err != nil {
		instances = nil // start fresh if corrupted
	}

	// Remove stale entries (PID no longer alive).
	instances = pruneDead(instances)

	// Remove any existing entry for our ID (restart case).
	for i, inst := range instances {
		if inst.ID == info.ID || inst.PID == info.PID {
			instances = append(instances[:i], instances[i+1:]...)
			break
		}
	}

	instances = append(instances, info)
	return r.save(instances)
}

// Unregister removes this instance from the registry.
func (r *Registry) Unregister() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	instances, err := r.load()
	if err != nil {
		return nil // nothing to remove
	}

	for i, inst := range instances {
		if inst.ID == r.selfID {
			instances = append(instances[:i], instances[i+1:]...)
			break
		}
	}

	return r.save(instances)
}

// Discover returns all running instances (excluding self).
func (r *Registry) Discover() ([]InstanceInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	instances, err := r.load()
	if err != nil {
		return nil, err
	}

	instances = pruneDead(instances)

	// Exclude self.
	var others []InstanceInfo
	for _, inst := range instances {
		if inst.ID != r.selfID {
			others = append(others, inst)
		}
	}

	return others, nil
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
	}

	instances, err := r.load()
	if err != nil {
		return nil
	}

	for i, inst := range instances {
		if inst.ID == r.selfID {
			instances[i].Status = status
			break
		}
	}

	return r.save(instances)
}

func (r *Registry) instancesPath() string {
	return filepath.Join(r.dir, "instances.json")
}

func (r *Registry) load() ([]InstanceInfo, error) {
	data, err := os.ReadFile(r.instancesPath())
	if err != nil {
		return nil, err
	}
	var instances []InstanceInfo
	if err := json.Unmarshal(data, &instances); err != nil {
		return nil, err
	}
	return instances, nil
}

func (r *Registry) save(instances []InstanceInfo) error {
	data, err := json.MarshalIndent(instances, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.instancesPath(), data, 0644)
}

// pruneDead removes entries whose PIDs are no longer running.
func pruneDead(instances []InstanceInfo) []InstanceInfo {
	var alive []InstanceInfo
	for _, inst := range instances {
		if isPIDAlive(inst.PID) {
			alive = append(alive, inst)
		}
	}
	return alive
}

// isPIDAlive checks if a process with the given PID exists.
func isPIDAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Send signal 0 to check existence.
	err = proc.Signal(nil)
	return err == nil
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

	// Detect languages by file extension.
	langSet := make(map[string]bool)
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Skip hidden dirs and common non-project dirs.
		base := filepath.Base(path)
		if d.IsDir() && (strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
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
		return nil
	})
	for lang := range langSet {
		meta.Languages = append(meta.Languages, lang)
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

	// Check for tests.
	meta.HasTests = hasTestFiles(dir)

	return meta
}

func hasTestFiles(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.Contains(name, "_test.") || strings.Contains(name, "test.") || strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// GenerateInstanceID creates a unique ID for this ggcode process.
func GenerateInstanceID() string {
	hostname, _ := os.Hostname()
	pid := os.Getpid()
	return fmt.Sprintf("ggcode-%s-%d-%d", hostname, pid, time.Now().UnixNano())
}
