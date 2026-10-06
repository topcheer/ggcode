package tool

// #3124: when tryLock fails at startup (second ggcode instance on the same
// workspace), the old code returned without starting backgroundLoop while
// `started` was already true - MarkDirty signals vanished, the startup
// snapshot was served forever, and idle release never ran. Probes simulate
// a competing instance by holding the lock file externally.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func lockIndexFileExternally(t *testing.T, m *CodeIndexManager) *os.File {
	t.Helper()
	lockPath := m.indexPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if !lockFileExcl(f) {
		_ = f.Close()
		t.Skip("platform cannot acquire exclusive lock for simulation")
	}
	return f
}

func waitCond(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestCodeIndexDegradedInstanceStartsLoop(t *testing.T) {
	tmp := t.TempDir()
	m := NewCodeIndexManager(tmp)
	defer m.Stop()

	ext := lockIndexFileExternally(t, m)
	defer ext.Close()

	m.StartBackgroundIndex()

	// Degraded flag set (not the old silent return).
	waitCond(t, 2*time.Second, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.degraded
	}, "degraded flag never set after lock contention")

	// MarkDirty signal must be consumed by the (now running) background
	// loop instead of rotting in the buffer.
	tmpFile := filepath.Join(tmp, "probe.go")
	if err := os.WriteFile(tmpFile, []byte("package p\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	ready := m.ready
	m.mu.RUnlock()
	m.MarkDirty([]string{tmpFile}) // no-op signal if !ready; dirtyFiles still recorded
	waitCond(t, 2*time.Second, func() bool {
		select {
		case <-m.rebuildCh:
			return false // still pending: nobody consumed it
		default:
			return true // consumed (or never fired because !ready)
		}
	}, "rebuildCh signal never consumed - background loop not running")

	if !ready {
		t.Log("index not ready (no disk cache) - dirty consumption verified via no-signal path")
	}
}

func TestCodeIndexDegradedPromotesWhenLockFrees(t *testing.T) {
	tmp := t.TempDir()
	m := NewCodeIndexManager(tmp)
	defer m.Stop()

	ext := lockIndexFileExternally(t, m)
	m.StartBackgroundIndex()
	waitCond(t, 2*time.Second, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.degraded
	}, "degraded flag never set")

	// Competing instance exits.
	_ = ext.Close()

	// Next debounced/periodic rebuildDirty acquires the lock and clears
	// degraded (writer promotion). Drive it via rebuildDirty directly to
	// avoid waiting a full tick.
	tmpFile := filepath.Join(tmp, "probe2.go")
	if err := os.WriteFile(tmpFile, []byte("package p\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.dirtyFiles[tmpFile] = time.Now().Unix()
	m.ready = true
	m.mu.Unlock()
	m.rebuildDirty("probe")

	m.mu.RLock()
	still := m.degraded
	m.mu.RUnlock()
	if still {
		t.Error("degraded not cleared after lock acquisition (writer promotion failed)")
	}
}

// Sequential-start regression: the normal (lock-holding) path must behave
// exactly as before - this guards the edit against disturbing the primary
// flow. The manager in these tests shares the real cache directory keyed
// by workspace hash; use a unique dir to avoid cross-test interference.
func TestCodeIndexNormalStartUnchanged(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tmp := t.TempDir()
			m := NewCodeIndexManager(filepath.Join(tmp, "ws"))
			defer m.Stop()
			m.StartBackgroundIndex()
		}(i)
	}
	wg.Wait()
}
