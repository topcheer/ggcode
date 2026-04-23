package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	instancesDir = "instances"
)

// InstanceInfo describes a running ggcode instance in the same directory.
type InstanceInfo struct {
	PID               int       `json:"pid"`
	UUID              string    `json:"uuid"`
	StartedAt         time.Time `json:"startedAt"`
	HasActiveChannels bool      `json:"hasActiveChannels"`
}

// InstanceDetect manages per-directory instance registration and discovery.
// It uses PID files under .ggcode/instances/ to track running instances.
type InstanceDetect struct {
	mu         sync.Mutex
	info       InstanceInfo
	dir        string // absolute path to .ggcode/instances/
	registered bool

	// checkAlive is the function used to check if a PID is alive.
	// Defaults to isProcessAlive; overridable for testing.
	checkAlive func(pid int) bool
}

// NewInstanceDetect creates a new instance detector for the given workspace.
// workspace is the project root directory (where .ggcode/ lives).
func NewInstanceDetect(workspace string) *InstanceDetect {
	return &InstanceDetect{
		dir: filepath.Join(workspace, ".ggcode", instancesDir),
		info: InstanceInfo{
			PID:       os.Getpid(),
			UUID:      uuid.New().String(),
			StartedAt: time.Now(),
		},
		checkAlive: isProcessAlive,
	}
}

// Register writes this instance's PID file and cleans up stale entries.
// Returns a list of other live instances (sorted by StartedAt ascending)
// and an error if file operations fail.
func (d *InstanceDetect) Register() ([]InstanceInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Ensure directory exists
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create instances dir: %w", err)
	}

	// Clean stale entries and collect live ones
	others := d.cleanAndCollectLocked()

	// Write our PID file
	data, err := json.Marshal(d.info)
	if err != nil {
		return nil, fmt.Errorf("marshal instance info: %w", err)
	}
	pidFile := d.pidFilePath(d.info.PID, d.info.UUID)
	if err := os.WriteFile(pidFile, data, 0o644); err != nil {
		return nil, fmt.Errorf("write pid file: %w", err)
	}
	d.registered = true

	return others, nil
}

// Unregister removes this instance's PID file. Call on graceful shutdown.
func (d *InstanceDetect) Unregister() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.registered {
		return
	}

	pidFile := d.pidFilePath(d.info.PID, d.info.UUID)
	os.Remove(pidFile) // ignore error — best effort
	d.registered = false
}

// Info returns this instance's info.
func (d *InstanceDetect) Info() InstanceInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.info
}

// IsPrimary checks if this instance is the oldest among all running instances
// in the same directory. Must be called after Register().
func (d *InstanceDetect) IsPrimary() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return true // no instances dir → we're primary
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := d.readInstanceFile(filepath.Join(d.dir, entry.Name()))
		if err != nil {
			continue
		}
		if info.UUID == d.info.UUID {
			continue // ourselves
		}
		if info.StartedAt.Before(d.info.StartedAt) {
			return false // someone started before us
		}
	}
	return true
}

// ListInstances returns all live instances in the directory, including self.
// Sorted by StartedAt ascending (oldest first).
func (d *InstanceDetect) ListInstances() []InstanceInfo {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil
	}

	var result []InstanceInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := d.readInstanceFile(filepath.Join(d.dir, entry.Name()))
		if err != nil {
			continue
		}
		if !d.checkAlive(info.PID) {
			continue
		}
		result = append(result, info)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

// UpdateHasActiveChannels updates the hasActiveChannels flag in the PID file.
func (d *InstanceDetect) UpdateHasActiveChannels(active bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.info.HasActiveChannels = active
	data, err := json.Marshal(d.info)
	if err != nil {
		return fmt.Errorf("marshal instance info: %w", err)
	}
	pidFile := d.pidFilePath(d.info.PID, d.info.UUID)
	return os.WriteFile(pidFile, data, 0o644)
}

// cleanAndCollectLocked removes stale PID files and returns live other instances.
// Caller must hold d.mu.
func (d *InstanceDetect) cleanAndCollectLocked() []InstanceInfo {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil
	}

	var live []InstanceInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fullPath := filepath.Join(d.dir, entry.Name())
		info, err := d.readInstanceFile(fullPath)
		if err != nil {
			// Corrupted file — remove it
			os.Remove(fullPath)
			continue
		}

		// Skip ourselves (previous crash left a file with our PID but different UUID)
		if info.PID == d.info.PID && info.UUID != d.info.UUID {
			os.Remove(fullPath)
			continue
		}

		// Check if process is still alive
		if !d.checkAlive(info.PID) {
			os.Remove(fullPath)
			continue
		}

		live = append(live, info)
	}

	sort.Slice(live, func(i, j int) bool {
		return live[i].StartedAt.Before(live[j].StartedAt)
	})
	return live
}

// pidFilePath returns the file path for a given PID + UUID pair.
func (d *InstanceDetect) pidFilePath(pid int, id string) string {
	return filepath.Join(d.dir, fmt.Sprintf("%d-%s.json", pid, id[:8]))
}

// readInstanceFile reads and parses an instance info file.
func (d *InstanceDetect) readInstanceFile(path string) (InstanceInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return InstanceInfo{}, err
	}
	var info InstanceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return InstanceInfo{}, err
	}
	// Validate required fields
	if info.PID <= 0 || info.UUID == "" {
		return InstanceInfo{}, fmt.Errorf("invalid instance file: %s", path)
	}
	return info, nil
}

// isProcessAlive checks if a process with the given PID is alive.
// Uses signal 0 (no-op signal) to check without affecting the process.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal 0 doesn't kill the process — just checks existence + permission
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// parsePIDFromFilename extracts the PID from a filename like "12345-abc12345.json".
func parsePIDFromFilename(name string) (int, error) {
	base := strings.TrimSuffix(name, ".json")
	parts := strings.SplitN(base, "-", 2)
	if len(parts) < 1 {
		return 0, fmt.Errorf("invalid filename: %s", name)
	}
	return strconv.Atoi(parts[0])
}
