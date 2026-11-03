package tool

import (
	"sync"
	"testing"
	"time"
)

func TestIssue1199_RaceSafety(t *testing.T) {

	rgPath.Store((*string)(nil))
	rgLastCheck.Store(0)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rgAvailable()
		}()
	}
	wg.Wait()
}

func TestIssue1199_TimestampGating(t *testing.T) {

	rgPath.Store((*string)(nil))
	rgLastCheck.Store(0)

	origWindow := rgRecheckWindow
	defer func() { rgRecheckWindow = origWindow }()

	rgRecheckWindow = 1 * time.Second

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rgAvailable()
		}()
	}
	wg.Wait()

	lastCheck := rgLastCheck.Load()

	wg = sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rgAvailable()
		}()
	}
	wg.Wait()

	newLastCheck := rgLastCheck.Load()
	if newLastCheck != lastCheck {
		t.Error("expected rgLastCheck to remain unchanged within recheck window")
	}
}

func TestIssue1199_FastPathUnchanged(t *testing.T) {

	path := "/existing/rg"
	rgPath.Store(&path)
	rgLastCheck.Store(time.Now().Unix())

	origWindow := rgRecheckWindow
	defer func() { rgRecheckWindow = origWindow }()

	rgRecheckWindow = 0

	if !rgAvailable() {
		t.Fatal("expected rgAvailable to return true with cached path")
	}

	firstCheck := rgLastCheck.Load()
	_ = rgAvailable()
	secondCheck := rgLastCheck.Load()
	if firstCheck != secondCheck {
		t.Error("expected fast path to skip re-probe when path is cached")
	}
}

func TestIssue1199_InstallRGNoPanic(t *testing.T) {

	rgPath.Store((*string)(nil))
	rgLastCheck.Store(0)
	rgTrying.Store(false)

	installRG()

	if rgTrying.Load() {
		t.Error("expected rgTrying to be reset after installRG")
	}
}

func TestIssue1199_AtomicStoreLoad(t *testing.T) {

	rgPath.Store((*string)(nil))

	path := "/test/path/rg"
	rgPath.Store(&path)

	ptr := rgPath.Load()
	if ptr == nil {
		t.Fatal("expected non-nil pointer after store")
	}

	loadedPath := ptr.(*string)
	if *loadedPath != path {
		t.Errorf("expected %s, got %s", path, *loadedPath)
	}
}

func TestIssue1199_ReprobeAfterWindow(t *testing.T) {

	origWindow := rgRecheckWindow
	defer func() { rgRecheckWindow = origWindow }()

	rgLastCheck.Store(0)

	rgRecheckWindow = 50 * time.Millisecond

	_ = rgAvailable()
	firstCheck := rgLastCheck.Load()

	if firstCheck > 0 {

		_ = rgAvailable()
		secondCheck := rgLastCheck.Load()
		if secondCheck != firstCheck {
			t.Error("expected timestamp to remain unchanged within recheck window")
		}

		time.Sleep(rgRecheckWindow + 10*time.Millisecond)

		_ = rgAvailable()
		thirdCheck := rgLastCheck.Load()
		if thirdCheck <= secondCheck {
			t.Error("expected timestamp to update after recheck window expires")
		}
	}

}
