package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Skill supply-chain integrity (#skill-provenance-lock): a lock file pins
// the sha256 of every loaded skill/command file. On load, files recorded in
// the lock must match their hash; mismatches are refused (repo-cloned
// malicious skill injection, cf. SkillPoison/CoordPoison 2026).

const (
	SkillLockVersion   = 1
	SkillLockStrictEnv = "GGCODE_SKILL_LOCK_STRICT"
)

type SkillLockFile struct {
	Version int              `json:"version"`
	Skills  []SkillLockEntry `json:"skills"`
}

type SkillLockEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"` // absolute canonical path of the SKILL.md / command .md
	Source   string `json:"source"`
	SHA256   string `json:"sha256"`
	LockedAt string `json:"locked_at"`
}

// LockPathFor returns the project lock file path.
func LockPathFor(projectDir string) string {
	return filepath.Join(projectDir, ".ggcode", "skills-lock.json")
}

// HashSkillFile returns the sha256 hex of the file at path.
func HashSkillFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// LoadSkillLock reads the lock file; missing file returns nil,nil (unlocked
// mode), malformed content is an error so the caller can warn loudly.
func LoadSkillLock(path string) (*SkillLockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lock SkillLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("skills-lock.json malformed: %w", err)
	}
	return &lock, nil
}

// WriteSkillLock persists the lock file (0600, parent dirs created).
func WriteSkillLock(path string, lock *SkillLockFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// BuildLockEntries walks loader targets and hashes every PROJECT-source
// skill/command markdown file. User-source skills (the operator's own
// ~/.ggcode, ~/.agents) are self-managed and never pinned; the lock
// defends the project supply chain - the repo-cloned-injection surface.
func BuildLockEntries(targets []loadTarget) []SkillLockEntry {
	var entries []SkillLockEntry
	for _, t := range targets {
		if t.Source != SourceProject {
			continue
		}
		entriesFor, _ := filepath.Glob(filepath.Join(t.Dir, "*"))
		for _, p := range entriesFor {
			var name, path string
			if t.LoadedFrom == LoadedFromSkills {
				if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
					continue
				}
				name = filepath.Base(p)
				path = filepath.Join(p, "SKILL.md")
			} else {
				if !strings.HasSuffix(p, ".md") {
					continue
				}
				name = strings.TrimSuffix(filepath.Base(p), ".md")
				path = p
			}
			sum, err := HashSkillFile(path)
			if err != nil {
				continue
			}
			entries = append(entries, SkillLockEntry{
				Name: name, Path: path, Source: string(t.Source),
				SHA256: sum, LockedAt: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

// BuildLockEntries (loader form) hashes everything this loader would load.
func (l *Loader) BuildLockEntries() []SkillLockEntry {
	return BuildLockEntries(l.targets)
}

type skillLockState struct {
	byPath map[string]SkillLockEntry
	strict bool
	once   sync.Once
}

var lockWarnOnce sync.Once

// newSkillLockState resolves the project lock + strict flag once per Load.
func newSkillLockState(projectDir string) *skillLockState {
	lock, err := LoadSkillLock(LockPathFor(projectDir))
	if err != nil {
		debug.Log("skill-lock", "lock file unreadable, integrity checks disabled: %v", err)
		lock = nil
	}
	st := &skillLockState{byPath: map[string]SkillLockEntry{}, strict: os.Getenv(SkillLockStrictEnv) == "1"}
	if lock != nil {
		for _, e := range lock.Skills {
			st.byPath[e.Path] = e
		}
	} else {
		lockWarnOnce.Do(func() {
			debug.Log("skill-lock", "no skills-lock.json in project: skill integrity unverified (run `ggcode skill lock` to pin)")
		})
	}
	return st
}

// allow reports whether the file may load, enforcing hash match for locked
// paths; strict mode additionally refuses untracked project-source files.
func (s *skillLockState) allow(path, name, source string) bool {
	entry, locked := s.byPath[path]
	if locked {
		sum, err := HashSkillFile(path)
		if err != nil {
			debug.Log("skill-lock", "refusing %s: hash unreadable: %v", name, err)
			return false
		}
		if sum != entry.SHA256 {
			debug.Log("skill-lock", "REFUSED %s: integrity mismatch (expected %.8s, actual %.8s)", name, entry.SHA256, sum)
			return false
		}
		return true
	}
	// Untracked file.
	if s.strict && source == string(SourceProject) {
		debug.Log("skill-lock", "REFUSED untracked project skill %s (strict mode: pin it via `ggcode skill lock`)", name)
		return false
	}
	return true
}
