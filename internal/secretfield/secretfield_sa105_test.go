package secretfield

import "testing"

// TestLooksLikeSecretField covers the single-source secret-name heuristic
// (#2180) shared by config, tui, the provider panel and the desktop
// registry. The "key" pattern is the #2167 regression guard: a drift once
// claimed API_KEY coverage while the pattern list lacked "key".
func TestLooksLikeSecretField(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		// Each pattern in the table, in its bare form.
		{name: "bare secret", key: "secret", want: true},
		{name: "bare token", key: "token", want: true},
		{name: "bare password", key: "password", want: true},
		{name: "bare credential", key: "credential", want: true},
		{name: "bare key", key: "key", want: true},

		// #2167 regression: API key forms must match via the "key" pattern.
		{name: "API_KEY upper", key: "API_KEY", want: true},
		{name: "api_key lower", key: "api_key", want: true},
		{name: "vendored key", key: "ZAI_API_KEY", want: true},

		// Case-insensitive substring semantics.
		{name: "mixed case secret", key: "MySecretValue", want: true},
		{name: "upper token", key: "ACCESS_TOKEN", want: true},
		{name: "plural credentials", key: "google_credentials", want: true},
		{name: "password prefix", key: "userPasswordHash", want: true},

		// Substring matches beyond the exact words (by design, documented).
		{name: "keystore contains key", key: "keystore", want: true},
		{name: "max_tokens contains token", key: "max_tokens", want: true},
		{name: "tokenize contains token", key: "tokenize", want: true},
		{name: "keyboard contains key", key: "keyboard", want: true},

		// Negatives: common non-secret config fields.
		{name: "empty string", key: "", want: false},
		{name: "model", key: "model", want: false},
		{name: "endpoint", key: "endpoint", want: false},
		{name: "username", key: "username", want: false},
		{name: "timeout", key: "timeout", want: false},
		{name: "verbose", key: "verbose", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeSecretField(tt.key); got != tt.want {
				t.Errorf("LooksLikeSecretField(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
