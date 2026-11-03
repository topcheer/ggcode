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
	"time"

	"github.com/google/uuid"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	instancesDir = "instances"
)

type InstanceInfo struct {
	PID               int       `json:"pid"`
	UUID              string    `json:"uuid"`
	StartedAt         time.Time `json:"startedAt"`
	HasActiveChannels bool      `json:"hasActiveChannels"`
}

type InstanceDetect struct {
	mu         sync.Mutex
	info       InstanceInfo
	dir        string
	registered bool

	checkAlive func(pid int) bool
}

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

func (d *InstanceDetect) Register() ([]InstanceInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create instances dir: %w", err)
	}

	others := d.cleanAndCollectLocked()

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

func (d *InstanceDetect) Unregister() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.registered {
		return
	}

	pidFile := d.pidFilePath(d.info.PID, d.info.UUID)
	os.Remove(pidFile)
	d.registered = false
}

func (d *InstanceDetect) Info() InstanceInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.info
}

func (d *InstanceDetect) IsRegistered() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.registered
}

func (d *InstanceDetect) IsPrimary() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return true
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
			continue
		}
		if !d.checkAlive(info.PID) {
			continue
		}
		if info.StartedAt.Before(d.info.StartedAt) {
			return false
		}
	}
	return true
}

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

			os.Remove(fullPath)
			continue
		}

		if info.PID == d.info.PID && info.UUID != d.info.UUID {
			os.Remove(fullPath)
			continue
		}

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

func (d *InstanceDetect) pidFilePath(pid int, id string) string {
	return filepath.Join(d.dir, fmt.Sprintf("%d-%s.json", pid, id[:8]))
}

func (d *InstanceDetect) readInstanceFile(path string) (InstanceInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return InstanceInfo{}, err
	}
	var info InstanceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return InstanceInfo{}, err
	}

	if info.PID <= 0 || info.UUID == "" {
		return InstanceInfo{}, fmt.Errorf("invalid instance file: %s", path)
	}
	return info, nil
}

func isProcessAlive(pid int) bool {
	return util.IsProcessAlive(pid)
}

func parsePIDFromFilename(name string) (int, error) {
	base := strings.TrimSuffix(name, ".json")
	parts := strings.SplitN(base, "-", 2)
	if len(parts) < 1 {
		return 0, fmt.Errorf("invalid filename: %s", name)
	}
	return strconv.Atoi(parts[0])
}
