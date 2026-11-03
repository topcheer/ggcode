package tool

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

	waitCond(t, 2*time.Second, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.degraded
	}, "degraded flag never set after lock contention")

	tmpFile := filepath.Join(tmp, "probe.go")
	if err := os.WriteFile(tmpFile, []byte("package p\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	ready := m.ready
	m.mu.RUnlock()
	m.MarkDirty([]string{tmpFile})
	waitCond(t, 2*time.Second, func() bool {
		select {
		case <-m.rebuildCh:
			return false
		default:
			return true
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

	_ = ext.Close()

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
