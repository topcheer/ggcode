package cron

// #3341 (sa-245 audit): when the last recurring job is deleted, save()
// removed <store>.json but never <store>.json.flock - the same sidecar leak
// as #3338 on the session side. A real profile had 16 orphan .flock files
// in ~/.ggcode/cron-jobs/.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveEmptyStoreRemovesFlockSidecar(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "jobs.json")
	s := NewScheduler(func(string, bool) {}, storePath)

	// Create the only recurring job, then delete it -> next save() takes
	// the empty-store branch.
	job, err := s.Create("*/5 * * * *", "probe #3341", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("store file should exist after Create: %v", err)
	}
	if _, err := os.Stat(storePath + ".flock"); err != nil {
		t.Fatalf("flock sidecar should exist after a save: %v", err)
	}

	if ok, err := s.DeleteWithError(job.ID); !ok || err != nil {
		t.Fatalf("DeleteWithError: ok=%v err=%v", ok, err)
	}

	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Errorf("empty store .json must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(storePath + ".flock"); !os.IsNotExist(err) {
		t.Errorf("empty store .flock sidecar must be removed too (#3341), stat err=%v", err)
	}
}
