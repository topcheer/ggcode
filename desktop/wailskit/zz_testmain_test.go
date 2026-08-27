package wailskit

import (
	"os"
	"testing"
)

// #1161: wailskit tests were flaking under -race because InitAgent calls
// startA2A, which binds real sockets and spawns mDNS discovery goroutines.
// Those goroutines keep running after a test finishes and can race inside
// whatever bridge gets constructed next. TestMain flips the same escape
// hatch the production code honors in startA2A so every wailskit test runs
// without any A2A/lanchat side effects; nothing outside this package reads
// the variable.
func TestMain(m *testing.M) {
	os.Setenv("GGCODE_WAILSKIT_DISABLE_A2A", "1")
	code := m.Run()
	os.Unsetenv("GGCODE_WAILSKIT_DISABLE_A2A")
	os.Exit(code)
}
