package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

type usageInfo struct {
	FirstSeen time.Time `json:"first_seen,omitempty"`
	LastUsed  time.Time `json:"last_used,omitempty"`
	Uses      int       `json:"uses,omitempty"`
	Source    string    `json:"source,omitempty"`

	Actor string `json:"actor,omitempty"`

	Outcome string `json:"outcome,omitempty"`

	Conflicts int `json:"conflicts,omitempty"`

	Consumed int `json:"consumed,omitempty"`
}

type usageIndex struct {
	Version int                   `json:"version"`
	Entries map[string]*usageInfo `json:"entries"`
}

const usageFileName = ".usage.json"

const usageVersion = 1

const usageProvenanceUnknown = "unknown"

const usageDebounce = 10 * time.Minute

func (am *AutoMemory) usagePath() string {
	return filepath.Join(am.dir, usageFileName)
}

func (am *AutoMemory) loadUsage() usageIndex {
	idx := usageIndex{Version: usageVersion, Entries: make(map[string]*usageInfo)}
	data, err := os.ReadFile(am.usagePath())
	if err != nil {
		return idx
	}
	var parsed usageIndex
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Entries == nil {
		debug.Log("memory", "usage sidecar unreadable, resetting: %v", err)
		return idx
	}
	idx.Entries = parsed.Entries
	return idx
}

func (am *AutoMemory) saveUsage(idx usageIndex) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	path := am.usagePath()
	tmp, err := os.CreateTemp(am.dir, usageFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}

	muAny, _ := writeMu.LoadOrStore(path, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func (am *AutoMemory) RecordProvenance(key, source string) {
	am.RecordProvenanceActor(key, source, "")
}

func (am *AutoMemory) RecordProvenanceActor(key, source, actor string) {
	if source == "" {
		source = usageProvenanceUnknown
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	if rec, ok := idx.Entries[key]; ok && rec != nil {
		if rec.Source == "" {
			rec.Source = source
		}
		if rec.Actor == "" && actor != "" {
			rec.Actor = actor
		}
		if rec.FirstSeen.IsZero() {
			rec.FirstSeen = time.Now()
		}
	} else {
		now := time.Now()
		idx.Entries[key] = &usageInfo{FirstSeen: now, Source: source, Actor: actor}
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage provenance persist failed for %s: %v", key, err)
	}
}

func (am *AutoMemory) RecordOutcome(key, outcome string) {
	if outcome != "success" && outcome != "partial" && outcome != "failed" {
		return
	}
	safe := disambiguateKey(key, sanitizeKey(key))

	unlock, lockErr := util.FileLock(am.dir + ".lock")
	if lockErr != nil {
		debug.Log("memory", "usage lock unavailable, recording outcome unlocked: %v", lockErr)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	rec, ok := idx.Entries[safe]
	if !ok || rec == nil {
		rec = &usageInfo{FirstSeen: time.Now()}
		idx.Entries[safe] = rec
	}
	if rec.Outcome == outcome {
		return
	}
	rec.Outcome = outcome
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage outcome persist failed for %s: %v", safe, err)
	}
}

func (am *AutoMemory) RecordConflictLoss(keys []string) {
	if len(keys) == 0 {
		return
	}
	now := time.Now()
	var fresh []string
	for _, k := range keys {
		dk := "loss:" + k
		if last, ok := am.useOnce.Load(dk); ok {
			if now.Sub(last.(time.Time)) < usageDebounce {
				continue
			}
		}
		am.useOnce.Store(dk, now)
		fresh = append(fresh, k)
	}
	if len(fresh) == 0 {
		return
	}

	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked write: %v", err)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	for _, k := range fresh {
		rec := idx.Entries[k]
		if rec == nil {
			rec = &usageInfo{FirstSeen: now}
			idx.Entries[k] = rec
		}
		rec.Conflicts++
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "conflict-loss persist failed: %v", err)
	}
}

func (am *AutoMemory) RecordConsumption(keys []string) {
	if len(keys) == 0 {
		return
	}
	now := time.Now()
	var fresh []string
	for _, k := range keys {
		dk := "consum:" + k
		if v, ok := am.useOnce.Load(dk); ok {
			if last, ok2 := v.(time.Time); ok2 && now.Sub(last) < usageDebounce {
				continue
			}
		}
		am.useOnce.Store(dk, now)
		fresh = append(fresh, k)
	}
	if len(fresh) == 0 {
		return
	}

	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked write: %v", err)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	for _, k := range fresh {
		rec := idx.Entries[k]
		if rec == nil {
			rec = &usageInfo{FirstSeen: now}
			idx.Entries[k] = rec
		}
		rec.Consumed++
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "consumption persist failed: %v", err)
	}
}

func (am *AutoMemory) RecordUse(keys []string, source string) {
	if len(keys) == 0 {
		return
	}
	now := time.Now()
	var fresh []string
	for _, k := range keys {
		if last, ok := am.useOnce.Load(k); ok {
			if now.Sub(last.(time.Time)) < usageDebounce {
				continue
			}
		}
		am.useOnce.Store(k, now)
		fresh = append(fresh, k)
	}
	if len(fresh) == 0 {
		return
	}

	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked write: %v", err)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	for _, k := range fresh {
		rec := idx.Entries[k]
		if rec == nil {
			rec = &usageInfo{}
			idx.Entries[k] = rec
		}
		rec.Uses++
		rec.LastUsed = now
		if rec.FirstSeen.IsZero() {
			rec.FirstSeen = now
		}
		if rec.Source == "" {
			rec.Source = source
		}
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage record persist failed: %v", err)
	}
}

func (am *AutoMemory) ForgetUsage(key string) {

	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked forget: %v", err)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	if _, ok := idx.Entries[key]; !ok {
		return
	}
	delete(idx.Entries, key)
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage forget persist failed for %s: %v", key, err)
	}
}

func (am *AutoMemory) UsageOf(key string) (usageInfo, bool) {
	idx := am.loadUsage()
	rec, ok := idx.Entries[key]
	if !ok || rec == nil {
		return usageInfo{}, false
	}
	return *rec, true
}

func provenanceSuffix(info usageInfo) string {
	if info.Uses <= 0 {
		return ""
	}

	if info.Actor != "" {
		return fmt.Sprintf(" [uses=%d src=%s actor=%s]", info.Uses, info.Source, info.Actor)
	}
	return fmt.Sprintf(" [uses=%d src=%s]", info.Uses, info.Source)
}
