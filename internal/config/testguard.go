package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test HOME isolation guard.
//
// Incident: a wailskit test wrote its fixture value
// (https://new-url.example.com) into the developer's real
// ~/.ggcode/vendors.yaml, silently corrupting the active endpoint. Tests
// that touch the real user HOME are always bugs — fixtures leak into
// developer configs, and config mutations race with concurrently running
// ggcode instances.
//
// Rules enforced here:
//   - Inside a test binary (testing.Testing()), any config path derived
//     from an un-isolated HOME fails fast (panic from ConfigDir, error
//     from Save-family guards).
//   - Isolation = the test changed HOME (t.Setenv("HOME", t.TempDir()))
//     relative to the snapshot taken at process start, or set
//     GGCODE_TEST_ALLOW_REAL_HOME=1 to explicitly opt in.
//   - Production binaries never fire: testing.Testing() is false there.

// realHomeSnapshot captures the home directory as seen at process start
// (package init runs before any test can call t.Setenv). On Unix that is
// $HOME; on Windows, USERPROFILE.
var realHomeSnapshot = firstNonEmpty(os.Getenv("HOME"), os.Getenv("USERPROFILE"))

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// homeIsIsolatedForTest reports whether the current process is a test
// binary whose HOME has been redirected away from the real user home.
func homeIsIsolatedForTest() bool {
	if !testing.Testing() {
		return true // production: nothing to isolate
	}
	if os.Getenv("GGCODE_TEST_ALLOW_REAL_HOME") == "1" {
		return true // explicit opt-in
	}
	cur := firstNonEmpty(os.Getenv("HOME"), os.Getenv("USERPROFILE"))
	if cur == "" {
		// No home resolvable at all — nothing real to protect.
		return true
	}
	if realHomeSnapshot == "" {
		// Process started without a home (unusual); cannot tell isolated
		// from real. Fail open rather than bricking exotic CI sandboxes.
		return true
	}
	return filepath.Clean(cur) != filepath.Clean(realHomeSnapshot)
}

// guardRealHomeDir panics when a test binary derives config paths from the
// real, un-isolated user HOME. It is called from path roots such as
// ConfigDir so both reads and writes are covered.
func guardRealHomeDir(op string) {
	if homeIsIsolatedForTest() {
		return
	}
	panic(fmt.Sprintf(
		"config: %s would use the REAL user home %q from a test — isolate first: t.Setenv(\"HOME\", t.TempDir()) in the test (or a package TestMain), or set GGCODE_TEST_ALLOW_REAL_HOME=1 to opt in",
		op, realHomeSnapshot,
	))
}

// GuardRealHomePath returns an error when a test binary is about to write
// into the real user HOME. path-anchored: writes to explicit temp paths
// (the common t.Setenv-free pattern of passing a tempdir file into Load)
// pass; writes under the un-isolated real home fail.
func GuardRealHomePath(path, op string) error {
	if homeIsIsolatedForTest() {
		return nil
	}
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if underTempRoot(abs) {
		// On Windows the OS temp root lives under the user profile
		// (...\AppData\Local\Temp), so the home-prefix check below
		// would reject every t.TempDir() target. Explicit temp paths
		// are the documented pass-through pattern on every platform.
		return nil
	}
	home := filepath.Clean(realHomeSnapshot)
	if strings.HasPrefix(abs+string(os.PathSeparator), home+string(os.PathSeparator)) {
		return fmt.Errorf(
			"config: %s would write %q under the REAL user home %q from a test — isolate first: t.Setenv(\"HOME\", t.TempDir()), or set GGCODE_TEST_ALLOW_REAL_HOME=1 to opt in",
			op, path, home,
		)
	}
	return nil
}

// underTempRoot reports whether abs sits inside the current OS temp root.
// On Unix that is /tmp (never under $HOME); on Windows it is
// %LOCALAPPDATA%\Temp, which lives under the user profile, so the
// home-prefix check needs this explicit exemption.
func underTempRoot(abs string) bool {
	tmp := os.TempDir()
	if tmp == "" {
		return false
	}
	if t, err := filepath.Abs(tmp); err == nil {
		tmp = t
	} else {
		tmp = filepath.Clean(tmp)
	}
	return strings.HasPrefix(abs+string(os.PathSeparator), filepath.Clean(tmp)+string(os.PathSeparator))
}
