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

// Ask 1 of the issue: when the diagnosis lands on the genuine-outage
// branch, it must embed the zero-spawn saturation capture (fd usage, proc
// count, vm pressure) so the next occurrence pins the saturated resource
// from the failure report alone.
func TestSpawnPressureCapture(t *testing.T) {
	p := spawnPressure()
	if p == "" {
		t.Skip("no pressure metrics readable on this platform")
	}
	if !strings.HasPrefix(p, "[S-16 pressure] ") {
		t.Fatalf("pressure capture must carry the marker prefix, got: %s", p)
	}
	for _, want := range []string{"fds=", "procs="} {
		if !strings.Contains(p, want) {
			t.Fatalf("pressure capture must include %s, got: %s", want, p)
		}
	}
}

func TestSpawnEnoentDiagnosisLiveCwdCarriesPressure(t *testing.T) {
	got := spawnEnoentDiagnosis(t.TempDir())
	if !strings.Contains(got, "still exists") {
		t.Fatalf("live cwd: expected still-exists diagnosis, got: %s", got)
	}
	if spawnPressure() != "" && !strings.Contains(got, "[S-16 pressure]") {
		t.Fatal("genuine-outage branch must embed the pressure capture when metrics are readable")
	}
}
