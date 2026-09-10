package plugin

import (
	"fmt"
	"sync"
	"testing"
)

func TestMCPDisabledConcurrentReadWrite(t *testing.T) {
	// #1782 case 2: without this the 500 concurrent toggles below wrote
	// the developer's REAL ~/.ggcode/disabled_mcp.json (a random subset as
	// the final state) and leaked the package-global cache to other tests.
	t.Setenv("HOME", t.TempDir())
	mcpDisabledMu.Lock()
	mcpDisabledCache = nil
	mcpDisabledCacheOK = false
	mcpDisabledMu.Unlock()
	var wg sync.WaitGroup
	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				MCPDisabled("test")
			}
		}()
	}
	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				SetMCPDisabled(fmt.Sprintf("srv-%d-%d", n, j), true)
			}
		}(i)
	}
	wg.Wait()
}
