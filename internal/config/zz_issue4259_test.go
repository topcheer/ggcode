package config

import "testing"

// zz_issue4259_test.go - A2AConfig.EffectiveAPIKey must not fall back to the
// public built-in default key (DefaultA2AAPIKey) when any authentication
// mechanism (api_keys / oauth2 / oidc / mTLS) is explicitly configured.
// Previously an api_keys-only config still merged ggcode-lan-a2a-v1 into the
// server's accepted key list, letting anyone who read the source bypass the
// configured credentials.

func TestA2AEffectiveAPIKey_Issue4259(t *testing.T) {
	cases := []struct {
		name string
		auth A2AAuthConfig
		want string
	}{
		{
			name: "no auth configured falls back to LAN default",
			auth: A2AAuthConfig{},
			want: DefaultA2AAPIKey,
		},
		{
			name: "single api_key is returned as-is",
			auth: A2AAuthConfig{APIKey: "my-strong-secret"},
			want: "my-strong-secret",
		},
		{
			name: "api_keys-only config must NOT leak the default key",
			auth: A2AAuthConfig{APIKeys: []string{"my-strong-secret-9f3a"}},
			want: "",
		},
		{
			name: "oauth2-only config must NOT leak the default key",
			auth: A2AAuthConfig{OAuth2: &A2AOAuth2Config{}},
			want: "",
		},
		{
			name: "oidc-only config must NOT leak the default key",
			auth: A2AAuthConfig{OIDC: &A2AOIDCConfig{}},
			want: "",
		},
		{
			name: "mtls-only config must NOT leak the default key",
			auth: A2AAuthConfig{MTLS: &A2AMTLSConfig{}},
			want: "",
		},
		{
			name: "api_key wins over other mechanisms",
			auth: A2AAuthConfig{APIKey: "primary", APIKeys: []string{"secondary"}},
			want: "primary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := A2AConfig{Auth: tc.auth}
			if got := cfg.EffectiveAPIKey(); got != tc.want {
				t.Fatalf("EffectiveAPIKey() = %q, want %q", got, tc.want)
			}
		})
	}
}
