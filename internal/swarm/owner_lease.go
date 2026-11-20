package swarm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Cross-instance safety primitives (#5829).
//
// Team boards and manifests persist under <boardRoot>/swarm/teams/<teamID>/.
// Before persistent teams, team IDs were per-process sequential ("team-1"),
// so two ggcode instances on one machine collided on the same directory and
// a fresh instance silently inherited a dead fleet's board. Two fixes:
//
//   - random team IDs (team-<6hex>, collision-checked against disk), so a
//     fresh CreateTeam can never land on an existing directory;
//   - an owner lease (owner.json: bootID+PID+hostname+heartbeat) written
//     into the team dir and refreshed on every persist. Loading a foreign
//     board requires an explicit RestoreTeam, and only after the lease is
//     provably dead (same-host PID signal-0 failure, or heartbeat older
//     than ownerLeaseTTL).

// ownerLeaseTTL bounds how long a silent owner is presumed alive. Owners on
// other hosts (no PID probe possible) are dead only after this expires.
const ownerLeaseTTL = 5 * time.Minute

// ownerLease is the on-disk ownership record for a team directory.
type ownerLease struct {
	BootID    string    `json:"bootID"`
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	Heartbeat time.Time `json:"heartbeat"`
}

type leaseVerdict int

const (
	leaseDirMissing   leaseVerdict = iota // no team directory on disk
	leaseOurs                             // our own boot ID (same Manager instance)
	leaseLegacy                           // directory exists but has no owner.json (pre-P0 board)
	leaseAliveForeign                     // a different, live instance owns the directory
	leaseDead                             // owner provably or stale-dead — inheritable
)

func (v leaseVerdict) String() string {
	switch v {
	case leaseDirMissing:
		return "dir-missing"
	case leaseOurs:
		return "ours"
	case leaseLegacy:
		return "legacy"
	case leaseAliveForeign:
		return "alive-foreign"
	case leaseDead:
		return "dead"
	}
	return "unknown"
}

// randomHex returns n cryptographically random bytes hex-encoded (2n chars).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Degraded entropy fallback: time-based, still unique enough for the
		// collision-checked allocation paths that consume this.
		return fmt.Sprintf("%0*x", n*2, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (m *Manager) teamDir(teamID string) string {
	return filepath.Join(m.boardRoot(), "swarm", "teams", teamID)
}

func (m *Manager) ownerLeasePath(teamID string) string {
	return filepath.Join(m.teamDir(teamID), "owner.json")
}

// newTeamID allocates a random team-<6hex> ID that is free both in-memory
// and on disk. Collision probability is ~1/16M per pair; on collision we
// simply draw again (max 8 attempts, then fall back to a longer ID).
// CreateTeam must NEVER reuse an existing directory, even a dead one —
// inheriting a foreign board is an explicit RestoreTeam decision (#5829).
func (m *Manager) newTeamID() string {
	for attempt := 0; attempt < 8; attempt++ {
		id := "team-" + randomHex(3)
		m.mu.Lock()
		_, inMem := m.teams[id]
		m.mu.Unlock()
		if inMem {
			continue
		}
		if _, err := os.Stat(m.teamDir(id)); err == nil {
			debug.Log("swarm", "newTeamID: %s collides with an existing directory, retrying", id)
			continue
		}
		return id
	}
	return "team-" + randomHex(8)
}

// stampOwnerLease writes our lease over the team dir's owner.json (atomic
// tmp+rename). Used at CreateTeam and when a dead/legacy lease is inherited.
func (m *Manager) stampOwnerLease(teamID string) error {
	lease := ownerLease{
		BootID:    m.bootID,
		PID:       os.Getpid(),
		Hostname:  m.hostname,
		Heartbeat: time.Now(),
	}
	return writeOwnerLeaseFile(m.ownerLeasePath(teamID), &lease)
}

func writeOwnerLeaseFile(path string, lease *ownerLease) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readOwnerLeaseFile(path string) (*ownerLease, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lease ownerLease
	if err := json.Unmarshal(data, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

// refreshOwnerHeartbeat re-stamps the lease heartbeat if (and only if) the
// on-disk lease is ours or absent. A foreign lease is never clobbered by a
// heartbeat refresh — only explicit dead-inheritance rewrites it.
func (m *Manager) refreshOwnerHeartbeat(teamID string) {
	path := m.ownerLeasePath(teamID)
	lease, err := readOwnerLeaseFile(path)
	if err != nil || lease.BootID != m.bootID {
		// Missing/corrupt/foreign: write ours only when no lease exists at all
		// (the CreateTeam path); otherwise leave the foreign record untouched.
		if err != nil {
			if os.IsNotExist(err) {
				_ = m.stampOwnerLease(teamID)
			}
		}
		return
	}
	lease.Heartbeat = time.Now()
	lease.PID = os.Getpid()
	lease.Hostname = m.hostname
	if err := writeOwnerLeaseFile(path, lease); err != nil {
		debug.Log("swarm", "owner heartbeat refresh for %s failed: %v", teamID, err)
	}
}

// checkOwnerLease classifies the team directory's ownership for load
// decisions. Alive means: same-host PID answers signal-0, OR the heartbeat
// is fresher than ownerLeaseTTL (covers foreign hosts where no probe is
// possible). Our own boot ID is always usable. Legacy directories without
// owner.json are treated as dead-owner (inheritable) with a WARN at the
// call site.
func (m *Manager) checkOwnerLease(teamID string) leaseVerdict {
	path := m.ownerLeasePath(teamID)
	lease, err := readOwnerLeaseFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if _, derr := os.Stat(m.teamDir(teamID)); derr == nil {
				return leaseLegacy
			}
			return leaseDirMissing
		}
		debug.Log("swarm", "owner lease read for %s failed (%v); treating as dead", teamID, err)
		return leaseDead
	}
	if lease.BootID == m.bootID {
		return leaseOurs
	}
	if lease.alive(m.hostname) {
		return leaseAliveForeign
	}
	return leaseDead
}

// alive reports whether the lease holder should be presumed running.
func (l *ownerLease) alive(hostname string) bool {
	if l == nil || l.BootID == "" {
		return false
	}
	// Same-host PID probe is authoritative in BOTH directions: a live PID
	// means alive; a DEAD PID means the owner is provably gone. The
	// heartbeat is only a fallback for FOREIGN hosts where no probe is
	// possible - otherwise a same-host owner that died seconds after its
	// last heartbeat is misread as alive for up to ownerLeaseTTL, blocking
	// cold-start restore of its own teams (probe-catch on 2026-11-20).
	if l.Hostname == hostname {
		if l.PID > 0 {
			// Self-PID short-circuit (exec-restart self-deadlock): the restart
			// tool replaces the binary via exec, which PRESERVES the PID while
			// the new image's Manager mints a fresh bootID. The on-disk lease
			// then carries our own PID with a foreign bootID, and the probe
			// below finds "a live process" — us — forever refusing restore.
			// A lease whose PID is the caller's own PID can only be our own
			// exec predecessor (or a coincidental PID reuse after this lease's
			// TTL already expired elsewhere); either way it is inheritable.
			if l.PID == os.Getpid() {
				return false
			}
			if pidAlive(l.PID) {
				return true
			}
		}
		// Same host, probe failed: owner is gone regardless of heartbeat.
		return false
	}
	// Foreign host: no PID probe possible; fall back to heartbeat freshness.
	return time.Since(l.Heartbeat) < ownerLeaseTTL
}
