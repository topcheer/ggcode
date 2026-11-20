package swarm

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// One-shot migration channel for pre-scoping teams stuck in the global
// store (#5857, follow-up to the #5853 strict-scoping ruling).
//
// Teams created before owner-session stamps existed persist fine in the
// global store (~/.ggcode/swarm/teams) but carry no OwnerSessionID, so the
// strict session filter hides them and restore used to fail-closed on the
// missing stamp - permanently unreachable real teams. Migration model:
//
//   - no legacy ownership semantics return: visibility and auto-restore
//     stay strict-equality (the #5853 ruling is unchanged);
//   - an EXPLICIT team_restore is the user-visible one-shot migration:
//     the team is adopted into the current session (stamped) and, when
//     this manager runs a scoped board root, its directory is moved from
//     the global store into that root, the global copy removed afterwards
//     (single-store uniqueness);
//   - the owner lease gates everything: a live instance still writing the
//     global copy is never migrated, and a foreign-STAMPED manifest is
//     another session's invisible data, never migration material.

// boundOwnerSession reads the manager's session stamp under lock.
func (m *Manager) boundOwnerSession() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ownerSessionID
}

// migrateGlobalTeam moves teamID from the global store into this manager's
// board root when the two differ and the team lives only globally. It
// reports whether a migration happened (the caller should then re-read the
// manifest from its own store).
func (m *Manager) migrateGlobalTeam(teamID string) bool {
	cur := filepath.Clean(m.boardRoot())
	global := filepath.Clean(util.ConfigDir())
	if global == "" || cur == global {
		return false // single-store deployment: nothing to migrate
	}
	srcDir := filepath.Join(global, "swarm", "teams", teamID)
	gmf, err := readManifestFile(filepath.Join(srcDir, "manifest.json"))
	if err != nil {
		return false // not in the global store
	}
	dstDir := filepath.Join(cur, "swarm", "teams", teamID)
	if _, err := os.Stat(filepath.Join(dstDir, "manifest.json")); err == nil {
		return false // already present in this store - not a migration
	}
	if own := m.boundOwnerSession(); gmf.OwnerSessionID != "" && gmf.OwnerSessionID != own {
		debug.Log("swarm", "migration of %s refused: manifest is stamped for session %s (#5857 migrates only unstamped pre-scoping teams)", teamID, gmf.OwnerSessionID)
		return false
	}
	// Never move a directory a live instance is still writing: the lease
	// would travel with it, but the writer would recreate the global path
	// on its next persist and fork the team.
	if lease, lerr := readOwnerLeaseFile(filepath.Join(srcDir, "owner.json")); lerr == nil {
		if lease.BootID != m.bootID && lease.alive(m.hostname) {
			debug.Log("swarm", "migration of %s refused: global copy is held by a live instance (boot %s)", teamID, lease.BootID)
			return false
		}
	}
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		debug.Log("swarm", "migration of %s failed (mkdir under %s): %v", teamID, cur, err)
		return false
	}
	// Same filesystem: an atomic directory rename IS the migration. A
	// non-empty destination (earlier partial migration) makes rename
	// fail; the copy path then converges onto the existing directory.
	if rerr := os.Rename(srcDir, dstDir); rerr != nil {
		if cerr := copyTeamDir(srcDir, dstDir); cerr != nil {
			debug.Log("swarm", "migration copy of %s into %s failed: %v", teamID, cur, cerr)
			return false
		}
		if derr := os.RemoveAll(srcDir); derr != nil {
			debug.Log("swarm", "WARN migration of %s: global copy removal failed: %v", teamID, derr)
		}
	}
	m.stampMigratedTeam(teamID)
	debug.Log("swarm", "migrated pre-scoping team %s from the global store into %s (#5857)", teamID, cur)
	return true
}

// stampMigratedTeam binds a freshly migrated team to this session (the
// migration's adoption stamp). No-op without a bound session.
func (m *Manager) stampMigratedTeam(teamID string) {
	own := m.boundOwnerSession()
	if own == "" {
		return
	}
	if err := m.updateManifest(teamID, func(mf *TeamManifest) {
		mf.OwnerSessionID = own
	}); err != nil {
		debug.Log("swarm", "migration session stamp for %s failed: %v", teamID, err)
	}
}

// copyTeamDir recursively copies the team directory src into dst (regular
// files only; tmp artifacts are skipped). Each file lands via tmp+rename
// so a crashed migration never leaves a torn manifest or board behind.
func copyTeamDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() || filepath.Ext(path) == ".tmp" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		tmp := target + ".migrate-tmp"
		if werr := os.WriteFile(tmp, data, info.Mode().Perm()); werr != nil {
			return werr
		}
		return os.Rename(tmp, target)
	})
}
