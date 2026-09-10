package cron

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// --- Legacy types for migration from workspace-scoped to session-scoped ---

// workspaceBucket groups jobs under a workspace key (old format).
// MigratedTo is the #1531 crash-window tombstone: set to the target
// session-store path BEFORE the new store is written, so an instance that
// crashes between the two writes leaves a durable marker instead of letting
// the next session re-migrate (double fire). Absent in pre-#1531 stores
// (zero value "" = not migrated).
type workspaceBucket struct {
	Workspace  string    `json:"workspace"`
	Jobs       []jobJSON `json:"jobs"`
	MigratedTo string    `json:"migrated_to,omitempty"`
}

// oldStoreFile is the old top-level structure keyed by SHA256(workspace dir).
type oldStoreFile map[string]workspaceBucket

func workspaceKey(dir string) string {
	h := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("%x", h)
}

// MigrateWorkspaceJobs moves recurring jobs from the old workspace-scoped
// store file to the new per-session store file. It removes the migrated
// workspace bucket from the old file, ensuring each workspace's jobs are
// migrated exactly once (by the first instance that starts).
//
// If oldStorePath doesn't exist, the workspace has no bucket, or the new
// store already exists, migration is a no-op.
//
// #414 hardening:
//   - The whole read-check-migrate-write sequence runs under an exclusive
//     lock on the old store (flock on Unix, LockFileEx on Windows). Two
//     instances starting concurrently used to both pass the Stat check and
//     migrate the same recurring job into two session stores → cron double
//     firing.
//   - The new session store is written BEFORE the bucket is removed from
//     the old store. The old delete-first order lost jobs permanently when
//     the new-store write failed (disk full / permissions) with no rollback.
func MigrateWorkspaceJobs(oldStorePath, newSessionPath, workspaceDir string) {
	if oldStorePath == "" || newSessionPath == "" || workspaceDir == "" {
		return
	}

	// Serialize concurrent migrations across processes (#414 TOCTOU).
	// Lock file lives next to the old store so its lifecycle matches.
	lockPath := oldStorePath + ".migrate.lock"
	release, ok := acquireMigrationLock(lockPath)
	if !ok {
		debug.Log("cron", "MigrateWorkspaceJobs: another instance holds the migration lock; skipping")
		return
	}
	defer release()

	// If the session store already exists, this session was loaded before —
	// no migration needed (jobs were already migrated on a previous run).
	if _, err := os.Stat(newSessionPath); err == nil {
		return
	}

	// Read the old store file.
	data, err := os.ReadFile(oldStorePath)
	if err != nil {
		return // file doesn't exist, nothing to migrate
	}

	var sf oldStoreFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return // corrupted, skip
	}

	wsKey := workspaceKey(workspaceDir)
	bucket, ok := sf[wsKey]
	if !ok {
		return // no jobs for this workspace
	}

	// Write the migrated recurring jobs to the NEW session store FIRST (#414).
	// Only after that succeeds do we remove the bucket from the old store —
	// a failed write leaves the old store untouched and a later start can
	// retry, instead of losing the jobs forever.
	var migrated []jobJSON
	oneShotDropped := 0 // #1531 case C: one-shots are intentionally not migrated
	for _, j := range bucket.Jobs {
		if j.Recurring {
			migrated = append(migrated, j)
		} else {
			oneShotDropped++
		}
	}
	if oneShotDropped > 0 {
		debug.Log("cron", "MigrateWorkspaceJobs: dropping %d one-shot job(s) from workspace %s (one-shots are not migrated; they fire once and are gone)", oneShotDropped, workspaceDir)
	}

	// #1531 case B: the new-store write and the old-store removal were two
	// separate atomic writes with a crash window between them - a kill in
	// that gap left the bucket in place, so the NEXT session (different
	// store path, Stat no-op) re-migrated the same recurring jobs and both
	// sessions scheduled them (double fire). Tombstone-first closes it:
	// stamp the bucket with the migration target BEFORE writing the new
	// store. A later instance seeing a tombstone skips when the target
	// store exists (jobs durably migrated) and re-migrates only when the
	// target vanished (crashed before the new-store write landed).
	if bucket.MigratedTo != "" {
		if _, err := os.Stat(bucket.MigratedTo); err == nil {
			debug.Log("cron", "MigrateWorkspaceJobs: workspace %s already migrated to %s by a previous instance; skipping", workspaceDir, bucket.MigratedTo)
			return
		}
		// Tombstone points at a store that never landed (crash between the
		// two writes). Fall through and migrate into THIS session instead.
	}
	bucket.MigratedTo = newSessionPath
	sf[wsKey] = bucket
	if out, err := json.MarshalIndent(sf, "", "  "); err == nil {
		if err := util.AtomicWriteFile(oldStorePath, out, 0644); err != nil {
			debug.Log("cron", "MigrateWorkspaceJobs: failed to write tombstone: %v (proceeding; the crash window stays open this once)", err)
		}
	}
	if len(migrated) > 0 {
		ss := sessionStore{Jobs: migrated}
		out, err := json.MarshalIndent(ss, "", "  ")
		if err != nil {
			debug.Log("cron", "MigrateWorkspaceJobs: failed to marshal migrated jobs: %v", err)
			return
		}
		if err := os.MkdirAll(filepath.Dir(newSessionPath), 0755); err != nil {
			debug.Log("cron", "MigrateWorkspaceJobs: failed to create session store dir: %v", err)
			return
		}
		if err := util.AtomicWriteFile(newSessionPath, out, 0644); err != nil {
			debug.Log("cron", "MigrateWorkspaceJobs: failed to write migrated jobs to %s: %v (bucket kept in old store for retry)", newSessionPath, err)
			return
		}
		debug.Log("cron", "MigrateWorkspaceJobs: migrated %d recurring jobs from workspace %s to session store", len(migrated), workspaceDir)
	}

	// Now that the jobs are safely in the new store, remove this workspace
	// from the old store so no other instance migrates them again.
	delete(sf, wsKey)
	if len(sf) == 0 {
		if err := os.Remove(oldStorePath); err != nil && !os.IsNotExist(err) {
			debug.Log("cron", "MigrateWorkspaceJobs: failed to remove empty old store %s: %v", oldStorePath, err)
		}
	} else {
		out, err := json.MarshalIndent(sf, "", "  ")
		if err != nil {
			debug.Log("cron", "MigrateWorkspaceJobs: failed to marshal old store: %v", err)
			return
		}
		if err := util.AtomicWriteFile(oldStorePath, out, 0644); err != nil {
			// #440: keeping the bucket here meant a LATER instance with a
			// different session path would re-migrate the same recurring
			// jobs into its own store — both sessions then schedule the job
			// and it fires twice. The jobs are already durably in the new
			// store above, so invalidate the bucket in the on-disk old store
			// by force-removing it; an unreadable/partial old store is
			// preferable to duplicate firing.
			debug.Log("cron", "MigrateWorkspaceJobs: failed to write old store back: %v (removing old store %s to prevent duplicate migration)", err, oldStorePath)
			if rmErr := os.Remove(oldStorePath); rmErr != nil && !os.IsNotExist(rmErr) {
				debug.Log("cron", "MigrateWorkspaceJobs: failed to remove old store %s: %v", oldStorePath, rmErr)
			}
		}
	}
}
