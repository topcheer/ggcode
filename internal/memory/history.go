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

const (
	historyDirName = ".history"

	historyTSFormat = "20060102T150405.000000000"
)

func (am *AutoMemory) archiveVersion(safe, path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	hdir := filepath.Join(am.dir, historyDirName)
	if err := os.MkdirAll(hdir, 0755); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format(historyTSFormat)
	dst := filepath.Join(hdir, safe+"."+stamp+".md")

	if _, err := os.Stat(dst); err == nil {
		dst = filepath.Join(hdir, fmt.Sprintf("%s.%s.%d.md", safe, stamp, os.Getpid()))
	}
	if err := os.Rename(path, dst); err != nil {
		return "", err
	}
	return dst, nil
}

type MemoryVersion struct {
	ModTime time.Time

	Path string

	Size int64
}

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

func (am *AutoMemory) HistoryVersions(key string) []MemoryVersion {
	return am.historyVersions(disambiguateKey(key, sanitizeKey(key)))
}

func (am *AutoMemory) tombstoneTimes(safe string) []time.Time {
	var times []time.Time
	if matches, gErr := filepath.Glob(filepath.Join(am.dir, historyDirName, safe+".tombstone.*")); gErr == nil {
		for _, m := range matches {
			if info, statErr := os.Stat(m); statErr == nil {
				times = append(times, info.ModTime())
			}
		}
	}
	if info, statErr := os.Stat(filepath.Join(am.dir, historyDirName, safe+".tombstone")); statErr == nil {
		times = append(times, info.ModTime())
	}
	return times
}

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

	tombTimes := am.tombstoneTimes(safe)
	if len(versions) == 0 {
		return "", time.Time{}, false, nil
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].modTime.Before(versions[j].modTime) })

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

	for _, t := range tombTimes {
		if versions[chosen].modTime.Before(t) && !asOf.Before(t) {
			debug.Log("memory", "as_of %s falls in the deleted gap of %q", asOf.Format(time.RFC3339), key)
			return "", time.Time{}, false, nil
		}
	}
	return string(data), versions[chosen].modTime, true, nil
}
