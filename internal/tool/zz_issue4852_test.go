package tool

import (
	"os"
	"strings"
	"testing"
)

// #4852 (S-16 reopen): spawn-ENOENT failures must name their root cause.
// Go's os/exec reports a forked child's failed chdir using the BINARY path
// ("fork/exec /bin/sh: no such file or directory"), so a deleted cwd between
// the NearestLiveDir check and Start is indistinguishable from a genuine
// shell outage by surface form alone. The diagnosis lstats cmd.Dir at
// failure time and reports which of the two root causes it is.

func TestSpawnEnoentDiagnosisDeadCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got := spawnEnoentDiagnosis(dir)
	if !strings.Contains(got, "no longer exists") {
		t.Fatalf("dead cwd: expected \"no longer exists\" diagnosis, got: %s", got)
	}
	if !strings.Contains(got, "NearestLiveDir") {
		t.Fatalf("dead cwd: expected self-heal mechanism (NearestLiveDir) named, got: %s", got)
	}
}

func TestSpawnEnoentDiagnosisLiveCwd(t *testing.T) {
	got := spawnEnoentDiagnosis(t.TempDir())
	if !strings.Contains(got, "still exists") {
		t.Fatalf("live cwd: expected \"still exists\" diagnosis, got: %s", got)
	}
	if strings.Contains(got, "no longer exists") {
		t.Fatalf("live cwd: must not be misdiagnosed as deleted, got: %s", got)
	}
}

func TestSpawnEnoentDiagnosisEmptyDir(t *testing.T) {
	got := spawnEnoentDiagnosis("")
	if !strings.Contains(got, "inherited process cwd") {
		t.Fatalf("empty dir: expected inherited-cwd note, got: %s", got)
	}
}
