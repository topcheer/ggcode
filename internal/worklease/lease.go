// Package worklease implements advisory, TTL-backed file-domain leases that
// let concurrent ggcode agents coordinate access to shared paths in a
// workspace. It realizes the "lease-based workspace lock" coordination
// pattern from multi-agent research (AgentRoom 2026 concurrent multi-agent
// coding; lease-based workspace locks with TTL claims, path scopes,
// heartbeats and stale-lock recovery): an agent declares the path scopes it
// intends to touch before working, other agents consult the registry and
// warn on overlap, and claims of crashed agents self-recover via TTL expiry
// without a central orchestrator.
//
// The registry is a single JSON state file (<workspaceRoot>/.ggcode/
// leases.json) shared by every worktree of the repository. Everything here
// is advisory: nothing hard-blocks a write, and every failure degrades to
// the unlocked path (fail-open) - coordination value must never cost the
// primary workflow.
package worklease

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// DefaultTTL is the lease lifetime used when Acquire or Heartbeat receives a
// non-positive ttl. Long enough for a focused task round, short enough that
// a crashed agent's claims self-recover without manual cleanup.
const DefaultTTL = 2 * time.Hour

// Lease kinds recorded in the registry.
const (
	KindWorktree = "worktree" // auto-registered by enter_worktree
	KindDomain   = "domain"   // explicit file-domain claim
)

// ErrNoLease is returned by Heartbeat when the owner holds no active lease.
var ErrNoLease = errors.New("worklease: no active lease for owner")

// Lease is one advisory claim on a set of path scopes. Scopes are
// repo-relative and slash-normalized: exact files as-is, directories with a
// trailing "/", glob patterns containing *, ? or [. An empty directory
// scope ("") leases the whole workspace.
type Lease struct {
	ID          string    `json:"id"`
	Owner       string    `json:"owner"`
	Kind        string    `json:"kind,omitempty"`
	Scopes      []string  `json:"scopes"`
	Note        string    `json:"note,omitempty"`
	AcquiredAt  time.Time `json:"acquired_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Conflict describes one overlap between a claimed scope and a scope held
// by a different owner.
type Conflict struct {
	Owner     string    `json:"owner"`
	LeaseID   string    `json:"lease_id"`
	Scope     string    `json:"scope"`
	Against   string    `json:"against"`
	ExpiresAt time.Time `json:"expires_at"`
}

type scopeKind int

const (
	kindExact scopeKind = iota
	kindDir
	kindGlob
)

// Manager operates the lease registry rooted at a workspace directory
// (typically the main repository root, shared by all its worktrees).
type Manager struct {
	// Dir is the workspace root holding .ggcode/leases.json.
	Dir string
	// Now injects the clock for tests; nil means time.Now.
	Now func() time.Time
}

// Acquire records a lease for owner on the given scopes. Any previous lease
// held by the same owner is replaced (re-acquiring is an idempotent
// refresh). Conflicts against other owners' active leases are reported but
// do NOT prevent the acquire - callers decide whether to warn or abort.
// Expired leases are pruned as a side effect (stale-lock recovery).
func (m *Manager) Acquire(owner, kind, note string, scopes []string, ttl time.Duration) (*Lease, []Conflict, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, nil, errors.New("worklease: empty owner")
	}
	norm := normalizeScopes(scopes)
	if len(norm) == 0 {
		return nil, nil, errors.New("worklease: no valid scopes")
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := m.now()

	unlock := m.tryLock()
	defer unlock()

	leases, err := m.load()
	if err != nil {
		return nil, nil, err
	}
	leases = pruneExpired(leases, now)
	conflicts := conflictsAgainst(leases, owner, norm)

	kept := make([]Lease, 0, len(leases)+1)
	for _, l := range leases {
		if l.Owner != owner {
			kept = append(kept, l)
		}
	}
	lease := Lease{
		ID:          fmt.Sprintf("%s@%d", owner, now.UnixNano()),
		Owner:       owner,
		Kind:        kind,
		Scopes:      norm,
		Note:        note,
		AcquiredAt:  now,
		HeartbeatAt: now,
		ExpiresAt:   now.Add(ttl),
	}
	leases = append(kept, lease)
	if err := m.save(leases); err != nil {
		return nil, nil, err
	}
	return &lease, conflicts, nil
}

// Active returns all non-expired leases. It is read-only: expired entries
// are filtered in memory and left on disk for the next write to prune.
func (m *Manager) Active() ([]Lease, error) {
	leases, err := m.load()
	if err != nil {
		return nil, err
	}
	return pruneExpired(leases, m.now()), nil
}

// Release drops every lease held by owner. It is idempotent: releasing an
// unknown owner succeeds without touching the registry.
func (m *Manager) Release(owner string) error {
	unlock := m.tryLock()
	defer unlock()

	leases, err := m.load()
	if err != nil {
		return err
	}
	now := m.now()
	leases = pruneExpired(leases, now)
	kept := make([]Lease, 0, len(leases))
	found := false
	for _, l := range leases {
		if l.Owner == owner {
			found = true
			continue
		}
		kept = append(kept, l)
	}
	if !found {
		return nil
	}
	return m.save(kept)
}

// Heartbeat extends the expiry of every lease held by owner to now+ttl,
// signalling the owner is still alive. Returns the latest new expiry.
func (m *Manager) Heartbeat(owner string, ttl time.Duration) (time.Time, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	unlock := m.tryLock()
	defer unlock()

	leases, err := m.load()
	if err != nil {
		return time.Time{}, err
	}
	now := m.now()
	leases = pruneExpired(leases, now)
	var latest time.Time
	found := false
	for i := range leases {
		if leases[i].Owner == owner {
			found = true
			leases[i].HeartbeatAt = now
			leases[i].ExpiresAt = now.Add(ttl)
			if leases[i].ExpiresAt.After(latest) {
				latest = leases[i].ExpiresAt
			}
		}
	}
	if !found {
		return time.Time{}, ErrNoLease
	}
	return latest, m.save(leases)
}

// Conflicts reports overlaps between the given scopes and active leases
// held by other owners. Claimed-but-unexpired own leases are ignored.
func (m *Manager) Conflicts(owner string, scopes []string) ([]Conflict, error) {
	norm := normalizeScopes(scopes)
	if len(norm) == 0 {
		return nil, nil
	}
	leases, err := m.Active()
	if err != nil {
		return nil, err
	}
	return conflictsAgainst(leases, owner, norm), nil
}

// CheckPath is Conflicts for a single path (handy for pre-write checks on
// an edit or file-op tool call).
func (m *Manager) CheckPath(owner, p string) ([]Conflict, error) {
	return m.Conflicts(owner, []string{p})
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) statePath() string {
	return filepath.Join(m.Dir, ".ggcode", "leases.json")
}

func (m *Manager) lockPath() string {
	return filepath.Join(m.Dir, ".ggcode", "leases.lock")
}

// tryLock serializes read-modify-write cycles between ggcode processes via
// the shared util.FileLock. Fail-open (same philosophy as #1834): if the
// advisory lock is unavailable, proceed unlocked - AtomicWriteFile still
// prevents torn files, worst case is a lost update between racing writers.
func (m *Manager) tryLock() func() {
	release, err := util.FileLock(m.lockPath())
	if err != nil {
		debug.Log("worklease", "registry lock unavailable (%v); proceeding unlocked", err)
		return func() {}
	}
	return release
}

func (m *Manager) load() ([]Lease, error) {
	data, err := os.ReadFile(m.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var leases []Lease
	if err := json.Unmarshal(data, &leases); err != nil {
		// Corrupt registry: treat as empty instead of poisoning every
		// future operation; the next save rewrites a clean file.
		debug.Log("worklease", "corrupt lease registry %s (%v); starting empty", m.statePath(), err)
		return nil, nil
	}
	return leases, nil
}

func (m *Manager) save(leases []Lease) error {
	if err := os.MkdirAll(filepath.Dir(m.statePath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(leases, "", "  ")
	if err != nil {
		return err
	}
	return util.AtomicWriteFile(m.statePath(), data, 0o644)
}

func pruneExpired(leases []Lease, now time.Time) []Lease {
	kept := make([]Lease, 0, len(leases))
	for _, l := range leases {
		if now.Before(l.ExpiresAt) {
			kept = append(kept, l)
		}
	}
	return kept
}

func conflictsAgainst(leases []Lease, owner string, scopes []string) []Conflict {
	var conflicts []Conflict
	for _, l := range leases {
		if l.Owner == owner {
			continue
		}
		for _, mine := range scopes {
			for _, theirs := range l.Scopes {
				if scopesOverlap(mine, theirs) {
					conflicts = append(conflicts, Conflict{
						Owner:     l.Owner,
						LeaseID:   l.ID,
						Scope:     mine,
						Against:   theirs,
						ExpiresAt: l.ExpiresAt,
					})
				}
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Owner != conflicts[j].Owner {
			return conflicts[i].Owner < conflicts[j].Owner
		}
		return conflicts[i].Scope < conflicts[j].Scope
	})
	return conflicts
}

func normalizeScopes(scopes []string) []string {
	norm := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if n, ok := normalizeScope(s); ok {
			norm = append(norm, n)
		}
	}
	return norm
}

// normalizeScope canonicalizes a user-provided scope. Directories are
// marked with a trailing "/", "./" prefixes are stripped, the workspace
// root ("." or "/") becomes the empty dir scope which overlaps everything,
// and scopes escaping the workspace ("../") are rejected.
func normalizeScope(s string) (string, bool) {
	s = strings.TrimSpace(filepath.ToSlash(s))
	if s == "" {
		return "", false
	}
	dir := strings.HasSuffix(s, "/")
	s = strings.TrimPrefix(s, "./")
	if s == "." || s == "/" {
		return "", true
	}
	s = path.Clean(s)
	if s == ".." || strings.HasPrefix(s, "../") {
		return "", false
	}
	if dir {
		s += "/"
	}
	return s, true
}

func kindOf(s string) scopeKind {
	switch {
	case s == "":
		return kindDir
	case strings.ContainsAny(s, "*?["):
		return kindGlob
	case strings.HasSuffix(s, "/"):
		return kindDir
	default:
		return kindExact
	}
}

// scopesOverlap reports whether two normalized scopes may address the same
// paths. It intentionally over-approximates around globs: advisory warnings
// may be conservative, but a missed conflict would be silent corruption.
func scopesOverlap(a, b string) bool {
	ka, kb := kindOf(a), kindOf(b)
	switch {
	case ka == kindExact && kb == kindExact:
		return a == b
	case ka == kindDir && kb == kindExact:
		return strings.HasPrefix(b, a)
	case ka == kindExact && kb == kindDir:
		return strings.HasPrefix(a, b)
	case ka == kindDir && kb == kindDir:
		return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
	case ka == kindGlob:
		return globOverlaps(a, b, kb)
	default:
		return globOverlaps(b, a, ka)
	}
}

func globOverlaps(g, other string, otherKind scopeKind) bool {
	p := globPrefix(g)
	switch otherKind {
	case kindExact:
		return strings.HasPrefix(other, p) || globMatches(g, other)
	case kindDir:
		// The workspace root dir scope ("") overlaps any glob.
		return other == "" || strings.HasPrefix(other, p) || strings.HasPrefix(p, other)
	default:
		q := globPrefix(other)
		return strings.HasPrefix(p, q) || strings.HasPrefix(q, p)
	}
}

// globPrefix returns the wildcard-free head of a glob pattern, the only
// part usable for cheap prefix reasoning.
func globPrefix(g string) string {
	if i := strings.IndexAny(g, "*?["); i >= 0 {
		return g[:i]
	}
	return g
}

func globMatches(g, name string) bool {
	ok, err := path.Match(g, name)
	return err == nil && ok
}

// FormatConflicts renders conflicts as an indented, deterministic, human-
// readable list for tool-result warnings.
func FormatConflicts(conflicts []Conflict) string {
	lines := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		lines = append(lines, fmt.Sprintf("  - %s leases %s (overlaps %s, expires %s)",
			c.Owner, displayScope(c.Scope), displayScope(c.Against), c.ExpiresAt.UTC().Format(time.RFC3339)))
	}
	return strings.Join(lines, "\n")
}

func displayScope(s string) string {
	switch kindOf(s) {
	case kindDir:
		if s == "" {
			return `"(workspace root)"`
		}
		return fmt.Sprintf("dir %q", s)
	case kindGlob:
		return fmt.Sprintf("glob %q", s)
	default:
		return fmt.Sprintf("%q", s)
	}
}
