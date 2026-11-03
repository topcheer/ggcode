package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	specPersistDir            = "cache"
	specPersistFile           = "speculate_patterns.json"
	specPersistMaxTransitions = 500
	specPersistInterval       = 5 * time.Minute
)

var (
	specLoadOnce          sync.Once
	specPersistedPatterns map[string]map[string]int
	specPersistMu         sync.Mutex
	specLastPersist       time.Time
)

func specPatternsPath() string {
	return filepath.Join(config.ConfigDir(), specPersistDir, specPersistFile)
}

func safeSpecPatternsPath() (path string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			path, ok = "", false
		}
	}()
	return specPatternsPath(), true
}

func loadSpecPatternsOnce() {
	specLoadOnce.Do(func() {
		path, ok := safeSpecPatternsPath()
		if !ok {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		var loaded map[string]map[string]int
		if err := json.Unmarshal(data, &loaded); err != nil {
			debug.Log("speculate", "persist: corrupt %s, starting fresh: %v", specPersistFile, err)
			return
		}
		specPersistedPatterns = loaded
		debug.Log("speculate", "persist: loaded %d pattern roots from %s", len(loaded), specPersistFile)
	})
}

func mergePersistedPatterns(s *speculator) {
	loadSpecPatternsOnce()
	if specPersistedPatterns == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for prev, nexts := range specPersistedPatterns {
		if s.patterns[prev] == nil {
			s.patterns[prev] = make(map[string]int)
		}
		for next, cnt := range nexts {
			if cnt < 1 {
				continue
			}
			s.patterns[prev][next] += cnt
		}
	}
}

func resetSpecPersistForTest() {
	specPersistMu.Lock()
	defer specPersistMu.Unlock()
	specLastPersist = time.Time{}
	specPersistedPatterns = nil
	specLoadOnce = sync.Once{}
}

func maybePersistSpecPatterns(s *speculator) {

	if s == nil {
		return
	}
	specPersistMu.Lock()
	if time.Since(specLastPersist) < specPersistInterval {
		specPersistMu.Unlock()
		return
	}
	specLastPersist = time.Now()
	specPersistMu.Unlock()

	s.mu.Lock()
	snapshot := make(map[string]map[string]int, len(s.patterns))
	for prev, nexts := range s.patterns {
		cp := make(map[string]int, len(nexts))
		for next, cnt := range nexts {
			cp[next] = cnt
		}
		snapshot[prev] = cp
	}
	s.mu.Unlock()

	trimmed := topTransitions(snapshot, specPersistMaxTransitions)

	path, ok := safeSpecPatternsPath()
	if !ok {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(trimmed)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return
	}
	debug.Log("speculate", "persist: saved %d roots to %s", len(trimmed), path)
}

func topTransitions(patterns map[string]map[string]int, limit int) map[string]map[string]int {
	type edge struct {
		prev string
		next string
		cnt  int
	}
	var edges []edge
	for prev, nexts := range patterns {
		for next, cnt := range nexts {
			edges = append(edges, edge{prev, next, cnt})
		}
	}
	if len(edges) <= limit {
		return patterns
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].cnt > edges[j].cnt })
	out := make(map[string]map[string]int)
	for _, e := range edges[:limit] {
		if out[e.prev] == nil {
			out[e.prev] = make(map[string]int)
		}
		out[e.prev][e.next] = e.cnt
	}
	return out
}
