package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #4469 mechanism pin: the fix must archive the FINAL live version and
// write a #4334-suffixed tombstone (not the legacy single path the repro
// logs about) - found=false must come from the contract, not from absence.
func TestGCForgottenDigestArchivesFinalAndWritesSuffixedTombstone(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	key := "build-process"
	am.SaveMemoryWithSourceActor(key, "v1 content", "user", "s")
	time.Sleep(10 * time.Millisecond)
	am.SaveMemoryWithSourceActor(key, "v2 content", "user", "s")
	am.ForgetKey(key)
	am.GarbageCollect()

	hdir := filepath.Join(dir, ".history")
	entries, err := os.ReadDir(hdir)
	if err != nil {
		t.Fatalf("history dir: %v", err)
	}
	var archivedV2, suffixedTomb bool
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, key+".tombstone.") {
			suffixedTomb = true
		}
		if strings.HasSuffix(n, ".md") {
			b, _ := os.ReadFile(filepath.Join(hdir, n))
			if strings.Contains(string(b), "v2 content") {
				archivedV2 = true
			}
		}
	}
	if !archivedV2 {
		t.Error("final live version (v2) was not archived by GC forget-digest - unrecoverable loss path still present")
	}
	if !suffixedTomb {
		t.Error("no timestamp-suffixed tombstone written by GC forget-digest")
	}
}
