package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// GCStats holds statistics from a garbage collection run.
// All counts are informational; GC never fails the session.
type GCStats struct {
	ExpiredRemoved   int // transient files past their TTL that were deleted from disk
	DedupRemoved     int // superseded evolving files that were deleted from disk
	ForgottenRemoved int // r10 user-forgotten files (forget.go) deleted from disk
	Total            int // total files scanned before GC
}

// GarbageCollect physically removes memory files that the curation logic
// considers expired or deduped. The existing LoadAll/LoadIndex/LoadForPrompt
// methods already filter these at load time, so this method is purely a disk
// cleanup optimisation — it prevents the memory directory from growing without
// bound across hundreds of sessions.
//
// GarbageCollect is safe to call at startup or periodically. It logs results
// via the debug package and never returns an error (disk cleanup is
// best-effort).
func (am *AutoMemory) GarbageCollect() GCStats {
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "GC: failed to read dir %s: %v", am.dir, err)
		return GCStats{}
	}

	now := time.Now()
	// #779: curation's count-cap is a PROMPT-layer throttle, not a deletion
	// verdict. Re-run the cap split so count-capped default entries are kept
	// on disk (they re-enter the active set as soon as older entries expire);
	// only expired transient and dedup losers are physically removed.
	active, _, _, _ := curateEntries(metas, now)
	promptActive, cappedEvicted := capByCountSplit(active, maxActiveMemories)

	// Build the disk whitelist: survivors of expiry+dedup AND cap evictions.
	activeKeys := make(map[string]bool, len(promptActive)+len(cappedEvicted))
	for _, m := range promptActive {
		activeKeys[m.Key] = true
	}
	for _, m := range cappedEvicted {
		activeKeys[m.Key] = true // capped ≠ dead: keep the file
	}

	// r10: user-forgotten entries (forget.go) lose their whitelist slot —
	// GC is the only physical deleter (#779 doctrine), so the forget mark
	// takes effect on disk here. RestoreKey before this run still saves
	// the entry; after it, only re-saving recreates it.
	// #4525: the forgotten set was read BEFORE the FileLock below, so a
	// RestoreKey completing in the lock-free window still got its key
	// archived+tombstoned by the stale snapshot (RestoreKey only touches
	// the sidecar - the md mtime guard cannot see it). Re-read the set
	// AFTER acquiring the lock: loadForgotten is a pure sidecar read with
	// no flock acquisition, so calling it under the held FileLock cannot
	// self-deadlock.
	unlockGC, lockErr := util.FileLock(am.dir + ".lock")
	gcLocked := lockErr == nil
	if !gcLocked {
		debug.Log("memory", "GC: filelock failed, degraded to unlocked delete: %v", lockErr)
	}
	forgotten := am.ForgottenSet()
	for k := range forgotten {
		delete(activeKeys, k)
	}
	var digestedForgotten, forgetUsageKeys []string

	stats := GCStats{Total: len(metas)}

	// #4416: hold the cross-process memory file lock across the re-stat
	// mtime guard AND the Remove below. SaveMemory renames under this same
	// lock (auto.go r401 GAP-B); without it, a rename landing in the
	// Stat→Remove micro-window passed the stale-mtime guard and the freshly
	// saved file was deleted permanently (the tmp had already been consumed
	// by the rename - no archive, no tombstone, unrecoverable). Fail-open
	// matches the save paths' posture (GC must not block on lock faults).
	// Released explicitly before the sidecar prune below, which acquires
	// the same lock via lockForgottenWrite (same-flock self-deadlock guard).
	for _, m := range metas {
		if activeKeys[m.Key] {
			continue
		}
		path := filepath.Join(am.dir, m.Key+".md")
		// #1613: the snapshot is stale by now - another instance (or this
		// process's own writes) may have touched the file between
		// collectMetas and here; removing per the stale snapshot physically
		// deleted freshly-written evolving memories. Re-stat and skip when
		// the file was modified after the snapshot was taken.
		if fi, err := os.Stat(path); err == nil && fi.ModTime().After(m.CreatedAt) {
			debug.Log("memory", "GC: %s modified after snapshot (concurrent writer); keeping", m.Key)
			continue
		}
		// #4469: a user-forgotten entry is a DeleteMemory-equivalent,
		// user-intent deletion - it must satisfy the same #3880 interval
		// contract. The old path did a plain os.Remove: the final live
		// version was never archived (unrecoverable loss) and no tombstone
		// was written, so ReadMemoryAsOf time-traveled past the forget to
		// stale .history content. Delete via archive+tombstone instead
		// (the archive rename IS the removal). On failure keep the forget
		// mark and retry next GC run - fail-closed beats silent data loss.
		if forgotten[m.Key] {
			if err := am.archiveAndTombstone(m.Key, path, m.Key); err != nil {
				debug.Log("memory", "GC: forget-digest contract delete failed for %s (will retry next run): %v", m.Key, err)
				continue
			}
			stats.ForgottenRemoved++
			// #4426: ForgetUsage itself FileLocks the SAME path (usage.go
			// r401) - calling it inside the GC lock window is a same-flock
			// self-conflict (flock conflicts per open-file-description, so
			// two fds in one process still block): acquireWithRetry busy-
			// waited the FULL 10s timeout per digested entry, fail-open,
			// and the held lock also dragged OTHER processes' writes into
			// their 10s timeouts. Defer to after unlockGC below, same
			// guard as the sidecar prune.
			forgetUsageKeys = append(forgetUsageKeys, m.Key)
			digestedForgotten = append(digestedForgotten, m.Key)
			continue
		}
		if err := os.Remove(path); err != nil {
			debug.Log("memory", "GC: failed to remove %s: %v", path, err)
			continue
		}
		if m.Category == CategoryTransient {
			stats.ExpiredRemoved++
		} else if m.Category == CategoryEvolving {
			stats.DedupRemoved++
		}
	}
	if gcLocked {
		unlockGC()
	}
	// #4426: outside the GC lock window - ForgetUsage re-acquires the lock
	// itself (drop sidecar ghosts, same as DeleteMemory).
	for _, k := range forgetUsageKeys {
		am.ForgetUsage(k)
	}

	// The forget sidecar must not keep marks for files GC just digested -
	// otherwise RestoreKey reports success for an entry that no longer
	// exists on disk (ghost-restore).
	if len(digestedForgotten) > 0 {
		// #4413: this prune is itself a sidecar RMW - run it under the same
		// write lock as ForgetKey/RestoreKey (acquired fresh: the GC lock
		// above was already released).
		unlock := am.lockForgottenWrite()
		f := am.loadForgotten()
		for _, k := range digestedForgotten {
			delete(f.Entries, k)
		}
		if err := am.saveForgotten(f); err != nil {
			debug.Log("memory", "GC: failed to prune forget sidecar: %v", err)
		}
		unlock()
	}

	removed := stats.ExpiredRemoved + stats.DedupRemoved + stats.ForgottenRemoved
	if removed > 0 {
		debug.Log("memory", "GC: removed %d files (%d expired, %d deduped, %d forgotten) from %s",
			removed, stats.ExpiredRemoved, stats.DedupRemoved, stats.ForgottenRemoved, am.dir)
	}

	return stats
}

// archiveAndTombstone implements the #3880 deletion contract shared by
// DeleteMemory and GC's forget-digest path (#4469): it archives the live
// version (the rename IS the removal - no separate os.Remove is needed)
// and writes a #4334 timestamp-suffixed tombstone so ReadMemoryAsOf sees
// the deletion gap instead of time-traveling to older archived content.
func (am *AutoMemory) archiveAndTombstone(safe, path, key string) error {
	// #4657: capture dst and roll the rename back if the tombstone write
	// fails. The rename IS the removal, so without a rollback a tombstone
	// fault (ENOSPC/EACCES/read-only remount) left the live file gone, no
	// tombstone, and an error to the caller - a permanent half-deleted state
	// that silently violates the #3880 invariant (as_of reads time-travel to
	// the archived "live" version) and makes the caller's retry report
	// "memory not found" (history.go:52 keeps the pre-delete mtime, so the
	// gap is invisible to ReadMemoryAsOf). Rolling back restores the live
	// file so a retry takes the normal, full path - the #3818 precedent
	// (history.go:58 "caller rolls back from dst if the swap-in fails").
	dst, err := am.archiveVersion(safe, path)
	if err != nil {
		return err
	}
	hdir := filepath.Join(am.dir, historyDirName)
	if err := os.MkdirAll(hdir, 0755); err != nil {
		am.rollbackArchive(path, dst, err)
		return err
	}
	// #4334: each deletion gets its OWN timestamp-suffixed tombstone. The
	// old single-path write overwrote the previous deletion instant on a
	// delete->recreate->delete sequence, so ReadMemoryAsOf could not see the
	// first deleted gap. The read side unions these with the legacy
	// single-tombstone file (which still records one valid instant).
	now := time.Now()
	tomb := filepath.Join(hdir, safe+".tombstone."+strconv.FormatInt(now.UnixNano(), 10))
	if err := os.WriteFile(tomb, []byte(key+"\n"), 0o644); err != nil {
		am.rollbackArchive(path, dst, err)
		return err
	}
	if err := os.Chtimes(tomb, now, now); err != nil {
		am.rollbackArchive(path, dst, err)
		return err
	}
	return nil
}

// rollbackArchive undoes archiveVersion's rename after a post-rename failure
// (#4657): the archived copy moves back to the live path. Best-effort - the
// fault is logged either way, and a rollback failure just means the memory
// stays archived (the pre-fix behavior); the caller still receives the
// original error.
func (am *AutoMemory) rollbackArchive(path, dst string, cause error) {
	if dst == "" {
		return // archiveVersion made no change (first-write case)
	}
	if err := os.Rename(dst, path); err != nil {
		debug.Log("memory", "archiveAndTombstone: tombstone fault %v AND rollback failed (%s -> %s): %v", cause, dst, path, err)
		return
	}
	debug.Log("memory", "archiveAndTombstone: tombstone fault %v, rolled back archive %s -> %s (memory left live; retry will redo the full delete)", cause, dst, path)
}

// DeleteMemory removes a single memory entry by key. Returns an error if the
// file does not exist or cannot be removed.
func (am *AutoMemory) DeleteMemory(key string) error {
	// #775: must resolve the filename exactly like SaveMemory, otherwise the
	// hash-suffixed file written for a non-injective key can never be deleted.
	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("memory %q not found", key)
	}
	// #3880: make the deletion visible to the as_of interval model.
	// Archive the live version (rename preserves its mtime, so pre-delete
	// as_of reads still resolve to it) and write a tombstone whose mtime is
	// the deletion instant; ReadMemoryAsOf returns found=false from that
	// moment. Without it, delete left only older archived versions and any
	// as_of AFTER the deletion time-traveled back to stale content -
	// breaking the "exact version that held at that instant" contract.
	if err := am.archiveAndTombstone(safe, path, key); err != nil {
		return err
	}
	// sa-85: drop the provenance/usage record so deleted keys do not
	// linger as ghost sidecar entries.
	am.ForgetUsage(safe)
	return nil
}

// GCFormatSummary returns a human-readable summary of GC results.
func (s GCStats) String() string {
	if s.ExpiredRemoved == 0 && s.DedupRemoved == 0 && s.ForgottenRemoved == 0 {
		return fmt.Sprintf("memory GC: %d files scanned, 0 removed", s.Total)
	}
	// Keep the pre-r10 format when nothing was forgotten-digested so
	// existing consumers (and tests) see byte-identical output.
	if s.ForgottenRemoved == 0 {
		return fmt.Sprintf("memory GC: %d files scanned, %d removed (%d expired, %d deduped)",
			s.Total, s.ExpiredRemoved+s.DedupRemoved, s.ExpiredRemoved, s.DedupRemoved)
	}
	return fmt.Sprintf("memory GC: %d files scanned, %d removed (%d expired, %d deduped, %d forgotten)",
		s.Total, s.ExpiredRemoved+s.DedupRemoved+s.ForgottenRemoved, s.ExpiredRemoved, s.DedupRemoved, s.ForgottenRemoved)
}
