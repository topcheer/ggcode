package intentions

import (
	"encoding/json"
	"fmt"
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

// Store persists intentions under the memory.intentions.* namespace as a
// single workspace-level JSON file. Every mutation re-reads the on-disk file
// under a cross-process flock before applying changes, so concurrent seats
// sharing one .ggcode/ do not double-capture or double-fire (#5260 watchdog
// review points 1-2). The flock sidecar discipline mirrors internal/cron.
type Store struct {
	mu   sync.Mutex
	path string
	// defaultOriginSession stamps newly captured intentions when the
	// capturing context did not supply an explicit origin session id.
	defaultOriginSession string
}

type storeFile struct {
	Namespace  string      `json:"namespace"` // always "memory.intentions"
	Intentions []Intention `json:"intentions"`
}

const storeNamespace = "memory.intentions"

// NewStore opens (or lazily creates) the intention store at path.
func NewStore(path, defaultOriginSession string) *Store {
	return &Store{path: path, defaultOriginSession: defaultOriginSession}
}

// DefaultStorePath returns the workspace-level store path for a ggcode home
// directory, mirroring the cron store placement convention.
func DefaultStorePath() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "intentions", "intentions.json")
}

// Put validates and persists a new intention, assigning ID, CreatedAt and
// the origin-session default. Duplicate suppression: a pending intention
// with the same statement+cue is rejected (#5260 AC-3 dedup).
func (s *Store) Put(in Intention) (Intention, error) {
	if s == nil {
		return in, fmt.Errorf("intentions: store not available")
	}
	if err := in.Validate(); err != nil {
		return in, err
	}
	if strings.TrimSpace(in.OriginSession) == "" {
		in.OriginSession = s.defaultOriginSession
	}
	in.ID = fmt.Sprintf("i-%x", time.Now().UnixNano())
	in.CreatedAt = time.Now().UTC()
	if in.State == "" {
		in.State = StatePending
	}
	if in.CooldownSec < 0 {
		in.CooldownSec = 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var result Intention
	err := s.mutateLocked(func(list *[]Intention) error {
		key := in.dedupKey()
		for _, ex := range *list {
			if ex.State == StatePending && ex.dedupKey() == key {
				return fmt.Errorf("intention: duplicate pending intention %q (family %s, pattern %q) already stored as %s", ex.Statement, ex.CueFamily, ex.CuePattern, ex.ID)
			}
		}
		*list = append(*list, in)
		result = in
		return nil
	})
	if err != nil {
		return Intention{}, err
	}
	return result, nil
}

// List returns intentions ordered by CreatedAt. Terminal states
// (realized/retired) are included only when includeTerminal is true.
func (s *Store) List(includeTerminal bool) []Intention {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.loadLocked()
	out := make([]Intention, 0, len(all))
	for _, in := range all {
		if !includeTerminal && (in.State == StateRealized || in.State == StateRetired) {
			continue
		}
		out = append(out, in)
	}
	return out
}

// FindByID returns a copy of the intention with the given id.
func (s *Store) FindByID(id string) (Intention, bool) {
	if s == nil {
		return Intention{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, in := range s.loadLocked() {
		if in.ID == id {
			return in, true
		}
	}
	return Intention{}, false
}

// Mutate applies fn to the stored intention with the given id under the
// cross-process lock, re-reading the file first so concurrent-seat updates
// are respected. fn returning an error aborts with no write.
func (s *Store) Mutate(id string, fn func(*Intention) error) error {
	if s == nil {
		return fmt.Errorf("intentions: store not available")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutateLocked(func(list *[]Intention) error {
		for i := range *list {
			if (*list)[i].ID == id {
				if err := fn(&(*list)[i]); err != nil {
					return err
				}
				return nil
			}
		}
		return fmt.Errorf("intention: %q not found", id)
	})
}

// Realize marks a pending/fired intention as realized (its goal was done).
func (s *Store) Realize(id string) error {
	return s.Mutate(id, func(in *Intention) error {
		if in.State != StatePending && in.State != StateFired {
			return fmt.Errorf("intention: cannot realize %q from state %s", id, in.State)
		}
		in.State = StateRealized
		return nil
	})
}

// Retire marks an intention obsolete from any non-terminal state.
func (s *Store) Retire(id string) error {
	return s.Mutate(id, func(in *Intention) error {
		if in.State == StateRealized || in.State == StateRetired {
			return fmt.Errorf("intention: %q already %s", id, in.State)
		}
		in.State = StateRetired
		return nil
	})
}

// mutateLocked loads the on-disk state under the flock, applies fn, and
// persists atomically (temp file + rename). s.mu must be held.
func (s *Store) mutateLocked(fn func(*[]Intention) error) error {
	if s.path == "" {
		return fmt.Errorf("intentions: store path is empty")
	}
	list := s.loadLocked()

	if err := fn(&list); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("intentions: create store dir: %w", err)
	}

	var unlock func()
	if lock, err := util.FileLock(s.path + ".flock"); err == nil {
		unlock = lock
		defer unlock()
	} else {
		debug.Log("intentions", "save: cross-process lock unavailable for %s: %v", s.path, err)
	}

	// Re-read under the lock and re-apply: concurrent seats may have written
	// between our load and the lock acquisition. fn is idempotent-safe for
	// the operations this package issues (dedup/append/state transitions).
	fresh := s.readRawLocked()
	if err := fn(&fresh); err != nil {
		return err
	}

	sort.Slice(fresh, func(i, j int) bool { return fresh[i].CreatedAt.Before(fresh[j].CreatedAt) })
	data, err := json.MarshalIndent(storeFile{Namespace: storeNamespace, Intentions: fresh}, "", "  ")
	if err != nil {
		return fmt.Errorf("intentions: marshal store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("intentions: write temp store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("intentions: rename store into place: %w", err)
	}
	return nil
}

// loadLocked reads the store file; missing or unparsable files yield an
// empty list (parse failures are logged, mirroring cron Scheduler.Load).
// s.mu must be held.
func (s *Store) loadLocked() []Intention {
	list := s.readRawLocked()
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	return list
}

func (s *Store) readRawLocked() []Intention {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		debug.Log("intentions", "load: failed to parse store file %s: %v", s.path, err)
		return nil
	}
	if sf.Namespace != "" && sf.Namespace != storeNamespace {
		debug.Log("intentions", "load: unexpected namespace %q in %s (want %q), refusing", sf.Namespace, s.path, storeNamespace)
		return nil
	}
	return sf.Intentions
}
