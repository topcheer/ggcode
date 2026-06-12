package tui

import "strings"

func removeEnv(env []string, key string) []string {
	prefix := key + "="
	out := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return out
}
