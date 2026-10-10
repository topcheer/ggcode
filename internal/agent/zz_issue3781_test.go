//go:build goolm

package agent

// #3781 probes: cross-package collision, non-go-test gating, save-failure
// reset ordering, and the positive same-test-twice path.

import (
	"os"
	"path/filepath"
	"testing"
)

const issue3781GoTestOut = `=== RUN   TestX
--- FAIL: TestX (0.00s)
FAIL	github.com/demo/pkgA	0.10s
=== RUN   TestX
--- FAIL: TestX (0.00s)
FAIL	github.com/demo/pkgB	0.11s
FAIL
`

func Test3781CrossPackageNoCollision(t *testing.T) {
	c := newTestFailCollector()
	c.record("go test ./...", issue3781GoTestOut)
	snap := c.snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 pkg-qualified fingerprints, got %v", snap)
	}
	if snap["github.com/demo/pkgA.TestX"] != 1 || snap["github.com/demo/pkgB.TestX"] != 1 {
		t.Fatalf("per-pkg counts wrong: %v", snap)
	}
	a := &Agent{testFails: c}
	for _, s := range a.collectWeaknessSignals() {
		if s.class == WeakForgetting {
			t.Fatalf("distinct-pkg single failures must not be WeakForgetting: %+v", s)
		}
	}
}

func Test3781NonGoTestNotCounted(t *testing.T) {
	c := newTestFailCollector()
	c.record("cat test-output.log", issue3781GoTestOut)
	c.record("grep FAIL build.log", issue3781GoTestOut)
	if len(c.snapshot()) != 0 {
		t.Fatalf("cat/grep output must not count: %v", c.snapshot())
	}
}

func Test3781SaveFailureStillResets(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only dir is writable for root")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.MkdirAll(filepath.Join(ro, ".ggcode"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0755)
	c := newTestFailCollector()
	c.record("go test ./...", "=== RUN   TestX\n--- FAIL: TestX (0.00s)\nFAIL\tpkg/x\t0.1s\nFAIL\n")
	a := &Agent{workingDir: ro, testFails: c}
	a.routeWeaknessSignals() // save must fail inside read-only dir
	if len(c.snapshot()) != 0 {
		t.Fatalf("counts must be cleared even when save fails: %v", c.snapshot())
	}
}

func Test3781SameTestTwiceStillForgetting(t *testing.T) {
	c := newTestFailCollector()
	out := "=== RUN   TestX\n--- FAIL: TestX (0.00s)\nFAIL\tpkg/x\t0.1s\nFAIL\n"
	c.record("go test ./...", out)
	c.record("go test ./...", out)
	if c.snapshot()["pkg/x.TestX"] != 2 {
		t.Fatalf("same test twice should count 2: %v", c.snapshot())
	}
	a := &Agent{testFails: c}
	found := false
	for _, s := range a.collectWeaknessSignals() {
		if s.fingerprint == "test-fail:pkg/x.TestX" && s.class == WeakForgetting {
			found = true
		}
	}
	if !found {
		t.Fatal("same-pkg same-test rerun must be WeakForgetting")
	}
}
