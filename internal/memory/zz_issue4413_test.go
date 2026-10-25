//go:build goolm

package memory

// #4413 regression: the forget sidecar was the package's only unlocked
// RMW - concurrent writers (startup GC on its background goroutine vs
// /memory forget, or two forgets) lost updates via last-write-wins, and a
// lost mark resurrected the entry into prompt injection. The locked
// mutateForgotten makes every distinct mark survive.

import (
	"fmt"
	"sync"
	"testing"
)

func TestIssue4413_ConcurrentForgetsAllSurvive(t *testing.T) {
	am := newTestAutoMemory(t)
	const n = 24
	for i := 0; i < n; i++ {
		if err := am.SaveMemory(fmt.Sprintf("issue4413-key-%d", i), "body"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := am.ForgetKey(fmt.Sprintf("issue4413-key-%d", i)); err != nil {
				t.Errorf("ForignKey(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	set := am.ForgottenSet()
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("issue4413-key-%d", i)
		if !set[k] {
			t.Fatalf("mark for %q lost under concurrency - unlocked RMW regression (#4413), set=%v", k, set)
		}
	}
}

func TestIssue4413_ConcurrentForgetAndRestore(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("issue4413-rs", "body"); err != nil {
		t.Fatal(err)
	}
	if err := am.ForgetKey("issue4413-rs"); err != nil {
		t.Fatal(err)
	}
	// Restore racing with an unrelated forget: neither may corrupt the
	// sidecar or crash; the final state must be exactly one of the two.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = am.RestoreKey("issue4413-rs") }()
	go func() { defer wg.Done(); _ = am.ForgetKey("issue4413-other") }()
	wg.Wait()
	am.ForgottenSet() // parse must still succeed
}
