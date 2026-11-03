package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var realHomeSnapshot = firstNonEmpty(os.Getenv("HOME"), os.Getenv("USERPROFILE"))

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func homeIsIsolatedForTest() bool {
	if !testing.Testing() {
		return true
	}
	if os.Getenv("GGCODE_TEST_ALLOW_REAL_HOME") == "1" {
		return true
	}
	cur := firstNonEmpty(os.Getenv("HOME"), os.Getenv("USERPROFILE"))
	if cur == "" {

		return true
	}
	if realHomeSnapshot == "" {

		return true
	}
	return filepath.Clean(cur) != filepath.Clean(realHomeSnapshot)
}

func guardRealHomeDir(op string) {
	if homeIsIsolatedForTest() {
		return
	}
	panic(fmt.Sprintf(
		"config: %s would use the REAL user home %q from a test — isolate first: t.Setenv(\"HOME\", t.TempDir()) in the test (or a package TestMain), or set GGCODE_TEST_ALLOW_REAL_HOME=1 to opt in",
		op, realHomeSnapshot,
	))
}

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
