package lsp

// Regression test for GitHub issue #1273: serverOverrides concurrent
// read/write must be guarded (unsynchronized Go map access is a fatal
// "concurrent map read and map write" crash). Run under -race for the real
// check; the assertions below pin the read-your-writes contract.

import (
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestServerOverridesConcurrentReadWrite(t *testing.T) {
	SetServerOverrides(map[string]config.LSPServerConfig{
		"go": {Binary: "/usr/local/bin/gopls"},
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				SetServerOverrides(map[string]config.LSPServerConfig{
					"go": {Binary: strings.Repeat("x", 1+n%3)},
				})
			}
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if m := ServerOverrides(); m == nil {
					t.Error("ServerOverrides must never observe a nil map after initialization")
					return
				}
				if _, ok := lookupServerOverride("go"); !ok {
					t.Error("lookupServerOverride must see the configured go override")
					return
				}
				// #1284: ResolveServerForWorkspace used to bare-read the map
				// (the one site the #1273 fix missed); exercise it under the
				// concurrent writer so -race guards this path too.
				_, _ = ResolveServerForWorkspace(t.TempDir())
			}
		}()
	}
	wg.Wait()
	// Read-your-writes after quiescence.
	SetServerOverrides(map[string]config.LSPServerConfig{
		"python": {Binary: "pyright-langserver"},
	})
	if ov, ok := lookupServerOverride("python"); !ok || ov.Binary != "pyright-langserver" {
		t.Fatalf("post-quiescence read-your-writes failed: %+v ok=%v", ov, ok)
	}
}
