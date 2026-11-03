package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const forgottenFileName = "user-forgotten.json"

type userForgotten struct {
	Entries map[string]time.Time `json:"entries"`
}

func (am *AutoMemory) loadForgotten() userForgotten {
	out := userForgotten{Entries: map[string]time.Time{}}
	data, err := os.ReadFile(filepath.Join(am.dir, forgottenFileName))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Entries == nil {
		debug.Log("memory", "forget: sidecar unreadable, treating as empty: %v", err)
		return userForgotten{Entries: map[string]time.Time{}}
	}
	return out
}

func (am *AutoMemory) saveForgotten(f userForgotten) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(am.dir, forgottenFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(am.dir, forgottenFileName))
}

func (am *AutoMemory) ForgottenSet() map[string]bool {
	f := am.loadForgotten()
	out := make(map[string]bool, len(f.Entries))
	for k := range f.Entries {
		out[k] = true
	}
	return out
}

func (am *AutoMemory) lockForgottenWrite() func() {
	path := filepath.Join(am.dir, forgottenFileName)
	muAny, _ := writeMu.LoadOrStore(path, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	unlock, err := util.FileLock(am.dir + ".lock")
	if err != nil {
		debug.Log("memory", "forget: filelock failed, degraded to process-local locking: %v", err)
		return mu.Unlock
	}
	return func() {
		unlock()
		mu.Unlock()
	}
}

func (am *AutoMemory) ForgetKey(key string) error {

	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")

	unlock := am.lockForgottenWrite()
	defer unlock()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("memory %q not found", key)
	}
	f := am.loadForgotten()
	if _, dup := f.Entries[safe]; dup {
		return nil
	}
	f.Entries[safe] = time.Now()
	if err := am.saveForgotten(f); err != nil {
		return err
	}
	debug.Log("memory", "forget: marked %q as user-forgotten (GC will digest)", safe)
	return nil
}

func (am *AutoMemory) RestoreKey(key string) error {
	safe := disambiguateKey(key, sanitizeKey(key))

	unlock := am.lockForgottenWrite()
	defer unlock()
	f := am.loadForgotten()
	if _, ok := f.Entries[safe]; !ok {
		return fmt.Errorf("memory %q is not forgotten", key)
	}
	delete(f.Entries, safe)
	return am.saveForgotten(f)
}

func (am *AutoMemory) ListForgotten() []string {
	f := am.loadForgotten()
	type kv struct {
		key string
		at  time.Time
	}
	items := make([]kv, 0, len(f.Entries))
	for k, at := range f.Entries {
		items = append(items, kv{k, at})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s (forgotten %s ago)", it.key, time.Since(it.at).Round(time.Minute)))
	}
	return out
}
