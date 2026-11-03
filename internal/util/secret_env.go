package util

import (
	"sort"
	"strings"
	"sync"
)

var (
	secretEnvMu  sync.RWMutex
	secretEnvSet = map[string]struct{}{}
)

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

func ResetSecretEnvForTests() {
	secretEnvMu.Lock()
	defer secretEnvMu.Unlock()
	secretEnvSet = map[string]struct{}{}
}
