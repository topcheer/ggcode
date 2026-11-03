package secretfield

import "strings"

func LooksLikeSecretField(key string) bool {
	lower := strings.ToLower(key)
	for _, pattern := range []string{"secret", "token", "password", "credential", "key"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
