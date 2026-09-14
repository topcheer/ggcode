package util

import (
	"reflect"
	"testing"
)

func TestSecretEnvRegistry(t *testing.T) {
	ResetSecretEnvForTests()
	defer ResetSecretEnvForTests()

	RegisterSecretEnv("ZAI_API_KEY", "ANTHROPIC_AUTH_TOKEN", "")
	if got := SecretEnvNames(); !reflect.DeepEqual(got, []string{"ANTHROPIC_AUTH_TOKEN", "ZAI_API_KEY"}) {
		t.Errorf("sorted names mismatch: %v", got)
	}

	env := []string{
		"PATH=/usr/bin",
		"ZAI_API_KEY=sk-secret",
		"HOME=/home/u",
		"ANTHROPIC_AUTH_TOKEN=tok",
	}
	got := StripSecretEnv(env)
	want := []string{"PATH=/usr/bin", "HOME=/home/u"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("strip mismatch:\n got %v\nwant %v", got, want)
	}
	// Input untouched
	if len(env) != 4 {
		t.Errorf("input slice mutated")
	}
}

func TestStripSecretEnvEmpty(t *testing.T) {
	ResetSecretEnvForTests()
	defer ResetSecretEnvForTests()

	if got := StripSecretEnv([]string{"A=1"}); !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Errorf("no-registry case must pass through, got %v", got)
	}
	if got := StripSecretEnv(nil); got != nil {
		t.Errorf("nil input must return nil, got %v", got)
	}
}
