//go:build goolm

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #3863 A: the go-test gate must reject commands that merely EMBED the
// words inside a quoted pattern (awk '/go test/{...}' ci.log, rg 'go test')
// while accepting real invocations (incl. env-prefixed and piped).
func TestIssue3863_GoTestTokenGate(t *testing.T) {
	logDump := "--- FAIL: TestX\nFAIL\tpkg/a\t0.1s\n"

	for _, bypass := range []string{
		`awk '/go test/{f=1} f' ci.log`,
		`sed -n '/go test/,$p' ci.log`,
		`rg 'go test' ci.log`,
		`cat ci.log | grep "go test"`,
	} {
		c := newTestFailCollector()
		c.record(bypass, logDump)
		if len(c.counts) != 0 {
			t.Fatalf("quoted-embedding bypass %q must not count, got %v", bypass, c.counts)
		}
	}

	for _, ok := range []string{
		"go test ./...",
		`GOFLAGS="-p=1" go test ./internal/agent/`,
		"cd /x && go test ./pkg/",
	} {
		c := newTestFailCollector()
		c.record(ok, logDump)
		if c.counts["pkg/a.TestX"] != 1 {
			t.Fatalf("real invocation %q must count, got %v", ok, c.counts)
		}
	}
}

// #3863 C: a truncated tail that lost its own FAIL<tab>pkg summary must
// fall back to the LAST resolved package instead of splitting the same
// test into a pkg-qualified and a bare fingerprint.
func TestIssue3863_TruncatedTailMergesToLastPkg(t *testing.T) {
	c := newTestFailCollector()
	out := "=== RUN TestX\n--- FAIL: TestX\nFAIL\tpkg/a\t0.1s\n" +
		"=== RUN TestX\n--- FAIL: TestX\n" // truncated: summary line lost
	c.record("go test ./...", out)
	if len(c.counts) != 1 || c.counts["pkg/a.TestX"] != 2 {
		t.Fatalf("truncated tail must merge into last pkg key, got %v", c.counts)
	}
	if len(c.degraded) != 0 {
		t.Fatalf("no degraded keys expected when a pkg summary existed, got %v", c.degraded)
	}
}

// #3863 B: a cross-run recurring environment-flaky test failure (store
// already holds WeakRare Count=1) must NOT upgrade to WeakForgetting.
// #3863 D: the same run seeds via the (now sole) first-sighting arm.
func TestIssue3863_TestFailNeverUpgradesToForgetting(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, ".ggcode", weaknessStoreFile)
	seed := weaknessSignalStore{
		"test-fail:pkg/a.TestX": {Class: WeakRare, Count: 1, LastTS: time.Now()},
		"errcat:cmdfail":        {Class: WeakRare, Count: 1, LastTS: time.Now()},
	}
	if err := saveWeaknessStore(storePath, seed); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(nil, nil, "sys", 5)
	a.workingDir = dir
	a.testFails = newTestFailCollector()
	a.testFails.record("go test ./...", "--- FAIL: TestX\nFAIL\tpkg/a\t0.1s\n")
	a.routeWeaknessSignals()

	after := loadWeaknessStore(storePath)
	if rec, ok := after["test-fail:pkg/a.TestX"]; ok {
		if rec.Class == WeakForgetting {
			t.Fatalf("cross-run bare test-fail recurrence must stay Rare (#3863 B), got %+v", rec)
		}
		if rec.Count < 2 {
			t.Fatalf("cross-run recurrence must still increment Count, got %+v", rec)
		}
	} else {
		t.Fatal("seeded test-fail record vanished")
	}
}
