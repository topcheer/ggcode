package config

// #2284-A phase 1: the wholesale keys.env→os.Setenv loop is gone - a
// keys.env value must NOT reach the process environment (any `bash env`
// dumped every vendor key in plaintext), while the anthropic bootstrap
// still resolves its trio through the runtime lookup map.

import (
	"os"
	"path/filepath"
	"testing"
)

func Test2284A_KeysEnvNotLeakedToProcessEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "sk-2284a-leak-canary"
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "keys.env"),
		[]byte("OPENAI_API_KEY_2284A="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A runtime env lookup must see the value...
	lookup := runtimeEnvLookup(nil)
	if v, ok := lookup("OPENAI_API_KEY_2284A"); !ok || v != secret {
		t.Fatalf("lookup must resolve keys.env values, got %q %v", v, ok)
	}
	// ...but it must NOT be sitting in the process environment.
	if v, ok := os.LookupEnv("OPENAI_API_KEY_2284A"); ok {
		t.Fatalf("keys.env value leaked into process env (present, len=%d)", len(v))
	}
}
