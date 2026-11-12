package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// goVersionCmd is a package var so tests can stub the toolchain probe.
var goVersionCmd = func() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return "go-version-unavailable"
	}
	return string(out)
}

// envFingerprint hashes the environment an agent's build/test observations
// depend on: dependency manifests (go.mod + go.sum) and the toolchain
// version. Round-31 research: env_drift.go covers only .env variable
// presence and harness_fingerprint.go only logs its own hash - nothing
// warned the agent that its environment had shifted under it.
func computeEnvFingerprint(workingDir string) string {
	h := sha256.New()
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join(workingDir, name))
		if err != nil {
			fmt.Fprintf(h, "%s:absent\n", name)
			continue
		}
		fmt.Fprintf(h, "%s:%x\n", name, sha256.Sum256(data))
	}
	fmt.Fprintf(h, "toolchain:%s\n", goVersionCmd())
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type envFingerprintState struct {
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updated_at"`
}

func envFingerprintPath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", "env-fingerprint.json")
}

// checkEnvDrift compares the live fingerprint against the persisted
// baseline. First run stores the baseline quietly; a mismatch stores the
// new fingerprint and returns a one-shot guidance text warning that prior
// build/test observations may be stale (inform-only, never blocks).
func checkEnvDrift(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	path := envFingerprintPath(workingDir)
	live := computeEnvFingerprint(workingDir)

	var stored envFingerprintState
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &stored) != nil || stored.Hash == "" {
		// First observation (or corrupted state): establish baseline, quiet.
		saveEnvFingerprint(path, live)
		return ""
	}
	if stored.Hash == live {
		return ""
	}
	saveEnvFingerprint(path, live)
	return fmt.Sprintf("[env-drift] environment fingerprint changed since the last run (%s -> %s): dependency manifests or toolchain version shifted. Prior build/test observations in this workspace may be stale - re-verify before relying on them.",
		stored.Hash, live)
}

func saveEnvFingerprint(path, hash string) {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.Marshal(envFingerprintState{Hash: hash, UpdatedAt: time.Now().UTC()})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0600) != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
