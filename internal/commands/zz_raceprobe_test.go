package commands

import "testing"

// Regression: loadDisabledSet's cache-miss path must hold the write lock,
// otherwise it races with PersistEnabledState's locked writes to
// disabledCache/disabledCacheOK (lost update + data race).
func TestLoadDisabledSetUnlockedWriteRace(t *testing.T) {
	disabledMu.Lock()
	savedCache, savedOK := disabledCache, disabledCacheOK
	disabledCache, disabledCacheOK = nil, false
	disabledMu.Unlock()
	t.Cleanup(func() {
		disabledMu.Lock()
		disabledCache, disabledCacheOK = savedCache, savedOK
		disabledMu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		PersistEnabledState("race-probe-skill", false)
	}()
	loadDisabledSet()
	<-done
}
