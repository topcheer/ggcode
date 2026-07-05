package plugin

import (
	"fmt"
	"sync"
	"testing"
)

func TestMCPDisabledConcurrentReadWrite(t *testing.T) {
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
