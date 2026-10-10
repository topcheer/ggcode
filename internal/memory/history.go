package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// r488 non-destructive memory (Mem++, arXiv:2610.02002): a revised decision
// arrives as a new document rather than an edit, so overwriting a memory key
// must not destroy the outgoing version. Before SaveMemoryWithSourceActor's
// atomic rename clobbers the current file, archiveVersion moves it into
// .history/<safe>.<ts>.md. The timestamp format is UTC, colon-free (Windows
// filenames cannot contain ':') and lexicographically sortable. Version
// ordering uses file mtimes (preserved by os.Rename across POSIX and NTFS),
// not the filename stamp - the stamp is for human browsing only.

const (
	historyDirName = ".history"
	// historyTSFormat: colon-free, fixed-width, UTC => lexicographic ==
	// chronological. Trailing zeros must be preserved for sortability, so
	// parse and format both use the same layout.
	historyTSFormat = "20060102T150405.000000000"
)

// archiveVersion moves the current file for `safe` into .history/ so the
// upcoming overwrite cannot destroy it. Idempotent on a missing file (first
// write). Errors are reported, never fatal: callers fail open (#3120
// precedent - memory writes must not block on archive faults) but log via
// debug so the loss is observable.
func (am *AutoMemory) archiveVersion(safe, path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil // first write, nothing to archive
		}
		return "", err
	}
	hdir := filepath.Join(am.dir, historyDirName)
	if err := os.MkdirAll(hdir, 0755); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format(historyTSFormat)
	dst := filepath.Join(hdir, safe+"."+stamp+".md")
	// Sub-microsecond collisions are impossible in practice (the caller holds
	// the per-path mutex and the cross-process file lock); if the target does
	// exist anyway, append a uniqueness suffix rather than clobber it.
	if _, err := os.Stat(dst); err == nil {
		dst = filepath.Join(hdir, fmt.Sprintf("%s.%s.%d.md", safe, stamp, os.Getpid()))
	}
	if err := os.Rename(path, dst); err != nil {
		return "", err
	}
	return dst, nil // #3818: caller rolls back from dst if the swap-in fails
}

// MemoryVersion is one archived (or current) snapshot of a memory key.
type MemoryVersion struct {
	// ModTime is when this version STARTED holding (rename preserves it, so
	// an archived file keeps its original write time).
	ModTime time.Time
	// Path is the archived file (.history/<safe>.<stamp>.md).
	Path string
	// Size of the version in bytes.
	Size int64
}

// historyVersions returns archived versions of `safe` sorted by ModTime
// ascending. Missing .history dir or foreign files are skipped silently -
// history is best-effort metadata, a corrupt archive must not break reads.
func (am *AutoMemory) historyVersions(safe string) []MemoryVersion {
	entries, err := os.ReadDir(filepath.Join(am.dir, historyDirName))
	if err != nil {
		return nil
	}
	prefix := safe + "."
	suffix := ".md"
	var out []MemoryVersion
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, MemoryVersion{
			ModTime: info.ModTime(),
			Path:    filepath.Join(am.dir, historyDirName, name),
			Size:    info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.Before(out[j].ModTime) })
	return out
}

// HistoryVersions is the exported view over historyVersions for tooling
// (list_memory renders archive depth per key). Sorted oldest-first.
func (am *AutoMemory) HistoryVersions(key string) []MemoryVersion {
	return am.historyVersions(disambiguateKey(key, sanitizeKey(key)))
}

// ReadMemoryAsOf returns the content of `key` that held at instant `asOf`
// (read-time temporal selection, Mem++'s core read primitive). The live file
// participates as the newest version; archived versions fill earlier
// intervals [version.ModTime, nextVersion.ModTime). Returns found=false when
// the key did not exist at asOf yet.
func (am *AutoMemory) ReadMemoryAsOf(key string, asOf time.Time) (content string, held time.Time, found bool, err error) {
	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")

	type snap struct {
		modTime time.Time
		path    string
	}
	var versions []snap
	for _, v := range am.historyVersions(safe) {
		versions = append(versions, snap{modTime: v.ModTime, path: v.Path})
	}
	if info, statErr := os.Stat(path); statErr == nil {
		versions = append(versions, snap{modTime: info.ModTime(), path: path})
	}
	if len(versions) == 0 {
		return "", time.Time{}, false, nil
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].modTime.Before(versions[j].modTime) })

	// Pick the latest version whose interval start is <= asOf.
	chosen := -1
	for i, v := range versions {
		if !v.modTime.After(asOf) {
			chosen = i
		}
	}
	if chosen < 0 {
		debug.Log("memory", "as_of %s predates first version of %q", asOf.Format(time.RFC3339), key)
		return "", time.Time{}, false, nil
	}
	data, readErr := os.ReadFile(versions[chosen].path)
	if readErr != nil {
		return "", time.Time{}, false, readErr
	}
	return string(data), versions[chosen].modTime, true, nil
}
