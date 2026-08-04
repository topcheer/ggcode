package tool

import (
	"os"
	"testing"
)

func TestNormalizedCommandEnv_ContainsOverrides(t *testing.T) {
	env := normalizedCommandEnv()

	// Verify all expected overrides are present.
	expected := map[string]string{
		"TERM=dumb":   "",
		"NO_COLOR=1":  "",
		"COLUMNS=120": "",
		"CI=true":     "",
	}
	found := make(map[string]bool)
	for _, e := range env {
		found[e] = true
	}
	for key := range expected {
		if !found[key] {
			t.Errorf("normalizedCommandEnv() missing expected entry: %s", key)
		}
	}
}

func TestNormalizedCommandEnv_OverridesTakePrecedence(t *testing.T) {
	// Temporarily set conflicting values in the real environment.
	os.Setenv("TERM", "xterm-256color")
	os.Setenv("NO_COLOR", "0")
	os.Setenv("COLUMNS", "40")
	os.Setenv("CI", "")
	defer func() {
		os.Unsetenv("TERM")
		os.Unsetenv("NO_COLOR")
		os.Unsetenv("COLUMNS")
		os.Unsetenv("CI")
	}()

	env := normalizedCommandEnv()

	// In Go's cmd.Env, later entries win. Verify our overrides come last.
	termValue := ""
	colorValue := ""
	colWidth := ""
	ciValue := ""
	for _, e := range env {
		if v, ok := matchEnvVar(e, "TERM"); ok {
			termValue = v
		}
		if v, ok := matchEnvVar(e, "NO_COLOR"); ok {
			colorValue = v
		}
		if v, ok := matchEnvVar(e, "COLUMNS"); ok {
			colWidth = v
		}
		if v, ok := matchEnvVar(e, "CI"); ok {
			ciValue = v
		}
	}

	if termValue != "dumb" {
		t.Errorf("TERM should be overridden to 'dumb', got %q", termValue)
	}
	if colorValue != "1" {
		t.Errorf("NO_COLOR should be overridden to '1', got %q", colorValue)
	}
	if colWidth != "120" {
		t.Errorf("COLUMNS should be overridden to '120', got %q", colWidth)
	}
	if ciValue != "true" {
		t.Errorf("CI should be overridden to 'true', got %q", ciValue)
	}
}

func TestNormalizedCommandEnv_PreservesUserEnv(t *testing.T) {
	// Set a custom env var that should be preserved.
	os.Setenv("GGCODE_TEST_VAR", "hello")
	defer os.Unsetenv("GGCODE_TEST_VAR")

	env := normalizedCommandEnv()

	found := false
	for _, e := range env {
		if e == "GGCODE_TEST_VAR=hello" {
			found = true
			break
		}
	}
	if !found {
		t.Error("normalizedCommandEnv() should preserve user environment variables")
	}
}

// matchEnvVar extracts the value part from a "KEY=VALUE" string for the given key.
func matchEnvVar(entry, key string) (string, bool) {
	prefix := key + "="
	if len(entry) > len(prefix) && entry[:len(prefix)] == prefix {
		return entry[len(prefix):], true
	}
	return "", false
}
