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
//
// HOME isolation: wailskit tests manipulate globalCfg and call
// UpdateConfig/Save paths that persist config files. Without isolation a
// test that reaches a Save leaks fixtures into the developer's real
// ~/.ggcode (incident: a base_url fixture value ended up in the real
// vendors.yaml, breaking the active endpoint). Redirect the whole package
// to a scratch home; internal/config's guard fails fast if anything still
// resolves the real one. Tests that need their own isolation keep using
// t.Setenv("HOME", ...) on top of this, which simply overrides it.
func TestMain(m *testing.M) {
	os.Setenv("GGCODE_WAILSKIT_DISABLE_A2A", "1")
	if home, err := os.MkdirTemp("", "wailskit-test-home-"); err == nil {
		defer os.RemoveAll(home)
		os.Setenv("HOME", home)
	}
	code := m.Run()
	os.Unsetenv("GGCODE_WAILSKIT_DISABLE_A2A")
	os.Exit(code)
}
