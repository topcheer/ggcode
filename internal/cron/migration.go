package cron

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
)

// --- Legacy types for migration from workspace-scoped to session-scoped ---

// workspaceBucket groups jobs under a workspace key (old format).
type workspaceBucket struct {
	Workspace string    `json:"workspace"`
	Jobs      []jobJSON `json:"jobs"`
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
func MigrateWorkspaceJobs(oldStorePath, newSessionPath, workspaceDir string) {
	if oldStorePath == "" || newSessionPath == "" || workspaceDir == "" {
		return
	}

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

	// Remove this workspace from the old store immediately.
	// This prevents concurrent instances from migrating the same jobs.
	delete(sf, wsKey)

	// Write back the old store (minus this workspace).
	if len(sf) == 0 {
		os.Remove(oldStorePath)
	} else {
		out, err := json.MarshalIndent(sf, "", "  ")
		if err == nil {
			os.WriteFile(oldStorePath, out, 0644)
		}
	}

	// Write migrated recurring jobs to the new session store.
	var migrated []jobJSON
	for _, j := range bucket.Jobs {
		if j.Recurring {
			migrated = append(migrated, j)
		}
	}
	if len(migrated) == 0 {
		return
	}

	ss := sessionStore{Jobs: migrated}
	out, err := json.MarshalIndent(ss, "", "  ")
	if err != nil {
		debug.Log("cron", "MigrateWorkspaceJobs: failed to marshal migrated jobs: %v", err)
		return
	}
	os.MkdirAll(filepath.Dir(newSessionPath), 0755)
	if err := os.WriteFile(newSessionPath, out, 0644); err != nil {
		debug.Log("cron", "MigrateWorkspaceJobs: failed to write migrated jobs to %s: %v", newSessionPath, err)
	} else {
		debug.Log("cron", "MigrateWorkspaceJobs: migrated %d recurring jobs from workspace %s to session store", len(migrated), workspaceDir)
	}
}
