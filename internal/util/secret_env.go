package util

import (
	"sort"
	"strings"
	"sync"
)

// secretEnvRegistry records the NAMES of environment variables that carry
// secrets ggcode itself injected into the process env (keys.env entries
// loaded wholesale, API keys set via /config setters or migration paths).
// Command tools strip these from child-process environments so an
// arbitrary `env`/`printenv` spawned by the agent cannot dump every
// configured credential in plaintext (#2284-A, R240 audit option 2).
//
// The registry is name-based and precise: only names config actively
// managed are stripped - no *_API_KEY heuristics that could break a
// user's own tooling.
var (
	secretEnvMu  sync.RWMutex
	secretEnvSet = map[string]struct{}{}
)

// RegisterSecretEnv records env var names as config-managed secrets.
// Safe for concurrent use; idempotent.
func RegisterSecretEnv(names ...string) {
	secretEnvMu.Lock()
	defer secretEnvMu.Unlock()
	for _, n := range names {
		if n == "" {
			continue
		}
		secretEnvSet[n] = struct{}{}
	}
}

// SecretEnvNames returns a sorted copy of the registered secret names.
func SecretEnvNames() []string {
	secretEnvMu.RLock()
	defer secretEnvMu.RUnlock()
	out := make([]string, 0, len(secretEnvSet))
	for n := range secretEnvSet {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// StripSecretEnv removes registered secret entries from an env slice
// (["NAME=value", ...]) and returns the filtered slice. Non-registered
// entries pass through untouched; the input is not modified.
func StripSecretEnv(env []string) []string {
	if len(env) == 0 {
		return env
	}
	secretEnvMu.RLock()
	if len(secretEnvSet) == 0 {
		secretEnvMu.RUnlock()
		return env
	}
	drop := make(map[string]struct{}, len(secretEnvSet))
	for n := range secretEnvSet {
		drop[n] = struct{}{}
	}
	secretEnvMu.RUnlock()

	out := make([]string, 0, len(env))
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		if _, bad := drop[name]; bad {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ResetSecretEnvForTests clears the registry (test isolation only).
func ResetSecretEnvForTests() {
	secretEnvMu.Lock()
	defer secretEnvMu.Unlock()
	secretEnvSet = map[string]struct{}{}
}
