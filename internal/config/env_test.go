package config

import (
	"os"
	"testing"
)

func TestHomeDir_RespectsEnv(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	got := HomeDir()
	if got != tmp {
		t.Errorf("HomeDir() = %q, want %q", got, tmp)
	}
}

func TestHomeDir_FallbackToOS(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	os.Unsetenv("HOME")

	got := HomeDir()

	if got == "/root" {
		t.Error("HomeDir() should not return /root as fallback")
	}
}

func TestConfigDir_RespectsHomeOverride(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	got := ConfigDir()
	want := tmp + "/.ggcode"
	if got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

// TestExpandShellEnvCrossReference pins #1803 case 2: rc-file variables
// referencing each other (A=1 / B=${A}) must resolve regardless of map
// iteration order - the old lookup kept the literal "${A}" half the runs.
func TestExpandShellEnvCrossReference(t *testing.T) {

	env := map[string]string{"PATH": "/usr/bin"}
	values := map[string]string{"A": "1", "B": "${A}"}
	expand := func(value string) string {
		return ExpandEnvWithLookup(value, func(key string) (string, bool) {
			if val, ok := env[key]; ok {
				return val, true
			}
			if val, ok := values[key]; ok {
				expanded := ExpandEnvWithLookup(val, func(k2 string) (string, bool) {
					v2, ok2 := env[k2]
					return v2, ok2
				})
				return expanded, true
			}
			return "", false
		})
	}
	for i := 0; i < 50; i++ {
		if got := expand(values["B"]); got != "1" {
			t.Fatalf("${A} cross-reference must resolve to 1 regardless of order, got %q", got)
		}
	}
}
