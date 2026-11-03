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

type workspaceBucket struct {
	Workspace  string    `json:"workspace"`
	Jobs       []jobJSON `json:"jobs"`
	MigratedTo string    `json:"migrated_to,omitempty"`
}

type oldStoreFile map[string]workspaceBucket

func workspaceKey(dir string) string {
	h := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("%x", h)
}

func MigrateWorkspaceJobs(oldStorePath, newSessionPath, workspaceDir string) {
	if oldStorePath == "" || newSessionPath == "" || workspaceDir == "" {
		return
	}

	lockPath := oldStorePath + ".migrate.lock"
	release, ok := acquireMigrationLock(lockPath)
	if !ok {
		debug.Log("cron", "MigrateWorkspaceJobs: another instance holds the migration lock; skipping")
		return
	}
	defer release()

	if _, err := os.Stat(newSessionPath); err == nil {
		return
	}

	data, err := os.ReadFile(oldStorePath)
	if err != nil {
		return
	}

	var sf oldStoreFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return
	}

	wsKey := workspaceKey(workspaceDir)
	bucket, ok := sf[wsKey]
	if !ok {
		return
	}

	var migrated []jobJSON
	oneShotDropped := 0
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

	if bucket.MigratedTo != "" {
		if _, err := os.Stat(bucket.MigratedTo); err == nil {
			debug.Log("cron", "MigrateWorkspaceJobs: workspace %s already migrated to %s by a previous instance; skipping", workspaceDir, bucket.MigratedTo)
			return
		}
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
			debug.Log("cron", "MigrateWorkspaceJobs: failed to write old store back: %v (removing old store %s to prevent duplicate migration)", err, oldStorePath)
			if rmErr := os.Remove(oldStorePath); rmErr != nil && !os.IsNotExist(rmErr) {
				debug.Log("cron", "MigrateWorkspaceJobs: failed to remove old store %s: %v", oldStorePath, rmErr)
			}
		}
	}
}
