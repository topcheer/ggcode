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

type GCStats struct {
	ExpiredRemoved   int
	DedupRemoved     int
	ForgottenRemoved int
	Total            int
}

func (am *AutoMemory) GarbageCollect() GCStats {
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "GC: failed to read dir %s: %v", am.dir, err)
		return GCStats{}
	}

	now := time.Now()

	active, _, _, _ := curateEntries(metas, now)
	promptActive, cappedEvicted := capByCountSplit(active, maxActiveMemories)

	activeKeys := make(map[string]bool, len(promptActive)+len(cappedEvicted))
	for _, m := range promptActive {
		activeKeys[m.Key] = true
	}
	for _, m := range cappedEvicted {
		activeKeys[m.Key] = true
	}

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

	for _, m := range metas {
		if activeKeys[m.Key] {
			continue
		}
		path := filepath.Join(am.dir, m.Key+".md")

		if fi, err := os.Stat(path); err == nil && fi.ModTime().After(m.CreatedAt) {
			debug.Log("memory", "GC: %s modified after snapshot (concurrent writer); keeping", m.Key)
			continue
		}

		if forgotten[m.Key] {
			if err := am.archiveAndTombstone(m.Key, path, m.Key); err != nil {
				debug.Log("memory", "GC: forget-digest contract delete failed for %s (will retry next run): %v", m.Key, err)
				continue
			}
			stats.ForgottenRemoved++

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

	for _, k := range forgetUsageKeys {
		am.ForgetUsage(k)
	}

	if len(digestedForgotten) > 0 {
		am.pruneDigestedForgotten(digestedForgotten)
	}

	removed := stats.ExpiredRemoved + stats.DedupRemoved + stats.ForgottenRemoved
	if removed > 0 {
		debug.Log("memory", "GC: removed %d files (%d expired, %d deduped, %d forgotten) from %s",
			removed, stats.ExpiredRemoved, stats.DedupRemoved, stats.ForgottenRemoved, am.dir)
	}

	return stats
}

func (am *AutoMemory) pruneDigestedForgotten(digested []string) {
	unlock := am.lockForgottenWrite()
	defer unlock()
	f := am.loadForgotten()
	pruned := 0
	for _, k := range digested {
		path := filepath.Join(am.dir, k+".md")
		if _, err := os.Stat(path); err == nil {

			debug.Log("memory", "GC: skip forget-mark prune for %s: live file re-created after digest (mark kept)", k)
			continue
		} else if !os.IsNotExist(err) {

			debug.Log("memory", "GC: skip forget-mark prune for %s: stat: %v", k, err)
			continue
		}
		delete(f.Entries, k)
		pruned++
	}
	if pruned == 0 {
		return
	}
	if err := am.saveForgotten(f); err != nil {
		debug.Log("memory", "GC: failed to prune forget sidecar: %v", err)
	}
}

func (am *AutoMemory) archiveAndTombstone(safe, path, key string) error {

	dst, err := am.archiveVersion(safe, path)
	if err != nil {
		return err
	}
	hdir := filepath.Join(am.dir, historyDirName)
	if err := os.MkdirAll(hdir, 0755); err != nil {
		am.rollbackArchive(path, dst, err)
		return err
	}

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

func (am *AutoMemory) rollbackArchive(path, dst string, cause error) {
	if dst == "" {
		return
	}
	if err := os.Rename(dst, path); err != nil {
		debug.Log("memory", "archiveAndTombstone: tombstone fault %v AND rollback failed (%s -> %s): %v", cause, dst, path, err)
		return
	}
	debug.Log("memory", "archiveAndTombstone: tombstone fault %v, rolled back archive %s -> %s (memory left live; retry will redo the full delete)", cause, dst, path)
}

func (am *AutoMemory) DeleteMemory(key string) error {

	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("memory %q not found", key)
	}

	if err := am.archiveAndTombstone(safe, path, key); err != nil {
		return err
	}

	am.ForgetUsage(safe)
	return nil
}

func (s GCStats) String() string {
	if s.ExpiredRemoved == 0 && s.DedupRemoved == 0 && s.ForgottenRemoved == 0 {
		return fmt.Sprintf("memory GC: %d files scanned, 0 removed", s.Total)
	}

	if s.ForgottenRemoved == 0 {
		return fmt.Sprintf("memory GC: %d files scanned, %d removed (%d expired, %d deduped)",
			s.Total, s.ExpiredRemoved+s.DedupRemoved, s.ExpiredRemoved, s.DedupRemoved)
	}
	return fmt.Sprintf("memory GC: %d files scanned, %d removed (%d expired, %d deduped, %d forgotten)",
		s.Total, s.ExpiredRemoved+s.DedupRemoved+s.ForgottenRemoved, s.ExpiredRemoved, s.DedupRemoved, s.ForgottenRemoved)
}
