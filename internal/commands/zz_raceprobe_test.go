package commands

import "testing"

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
