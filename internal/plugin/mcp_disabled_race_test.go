package plugin

import (
	"fmt"
	"sync"
	"testing"
)

func TestMCPDisabledConcurrentReadWrite(t *testing.T) {

	t.Setenv("HOME", t.TempDir())
	resetDisabledCache()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				MCPDisabledIn("", "test")
			}
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				SetMCPDisabledIn(fmt.Sprintf("ws-race-%d", n%2), fmt.Sprintf("srv-%d-%d", n, j), true)
			}
		}(i)
	}
	wg.Wait()
}
