package auth

// ---------------------------------------------------------------------------
// OAuth2/OIDC Provider Presets
// ---------------------------------------------------------------------------
//
// Each preset contains the public client_id, endpoint URLs, and default scopes
// for a well-known identity provider. Users select a preset by name in config
// and get a zero-config authentication experience.
//
// IMPORTANT: client_id values are PUBLIC (not secrets). They identify the
// application registration, not authenticate it. PKCE (Proof Key for Code
// Exchange) protects the token exchange without needing a client_secret.
//
// If a user wants to use a provider not listed here, or wants their own
// client_id (e.g., for a custom OAuth App), they simply set client_id and
// issuer_url directly and leave "provider" empty.

// OAuth2ProviderPreset contains the public configuration for an OAuth2 provider.
type OAuth2ProviderPreset struct {
	Name           string   // display name
	AuthorizeURL   string   // authorization endpoint
	TokenURL       string   // token endpoint
	DeviceAuthURL  string   // device authorization endpoint (empty if unsupported)
	UserInfoURL    string   // userinfo endpoint (for OIDC)
	OIDCDiscovery  string   // /.well-known/openid-configuration URL (empty if not OIDC)
	DefaultScopes  []string // recommended scopes
	SupportsPKCE   bool     // Authorization Code + PKCE
	SupportsDevice bool     // Device Authorization Flow
}

// Built-in provider presets.
// client_id is intentionally NOT included here — each ggcode installation
// should register its own OAuth App with the provider and configure the
// client_id in ggcode.yaml. This is because:
//
//  1. Each OAuth App is bound to specific redirect URIs
//  2. Each installation may have different domain/port
//  3. Provider terms of service may prohibit shared client_ids
//
// The preset only provides endpoint URLs and recommended scopes.
var ProviderPresets = map[string]OAuth2ProviderPreset{
	"github": {
		Name:           "GitHub",
		AuthorizeURL:   "https://github.com/login/oauth/authorize",
		TokenURL:       "https://github.com/login/oauth/access_token",
		DeviceAuthURL:  "https://github.com/login/device/code",
		DefaultScopes:  []string{"read:user", "user:email"},
		SupportsPKCE:   true,
		SupportsDevice: true,
	},
	"google": {
		Name:           "Google",
		AuthorizeURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:       "https://oauth2.googleapis.com/token",
		UserInfoURL:    "https://openidconnect.googleapis.com/v1/userinfo",
		OIDCDiscovery:  "https://accounts.google.com/.well-known/openid-configuration",
		DefaultScopes:  []string{"openid", "profile", "email"},
		SupportsPKCE:   true,
		SupportsDevice: false,
	},
	"auth0": {
		Name:           "Auth0",
		AuthorizeURL:   "https://AUTH0_TENANT.auth0.com/authorize", // placeholder
		TokenURL:       "https://AUTH0_TENANT.auth0.com/oauth/token",
		UserInfoURL:    "https://AUTH0_TENANT.auth0.com/userinfo",
		OIDCDiscovery:  "https://AUTH0_TENANT.auth0.com/.well-known/openid-configuration",
		DefaultScopes:  []string{"openid", "profile", "email"},
		SupportsPKCE:   true,
		SupportsDevice: true,
	},
	"azure": {
		Name:           "Azure AD",
		AuthorizeURL:   "https://login.microsoftonline.com/AZURE_TENANT/oauth2/v2.0/authorize",
		TokenURL:       "https://login.microsoftonline.com/AZURE_TENANT/oauth2/v2.0/token",
		DeviceAuthURL:  "https://login.microsoftonline.com/AZURE_TENANT/oauth2/v2.0/devicecode",
		OIDCDiscovery:  "https://login.microsoftonline.com/AZURE_TENANT/v2.0/.well-known/openid-configuration",
		DefaultScopes:  []string{"openid", "profile", "email"},
		SupportsPKCE:   true,
		SupportsDevice: true,
	},
}

// ResolveProviderPreset returns the preset for the given provider name.
// Returns nil if the provider is not found.
func ResolveProviderPreset(provider string) *OAuth2ProviderPreset {
	p, ok := ProviderPresets[provider]
	if !ok {
		return nil
	}
	return &p
}

// ResolveA2AAuth resolves the provider preset and merges with user config.
// If provider is set, endpoint URLs come from the preset. User can override
// client_id and scopes. If provider is empty, all fields must be set manually.
func ResolveA2AAuth(provider, clientID, issuerURL, scopes string) (authorizeURL, tokenURL, resolvedScopes string, err error) {
	if provider == "" {
		// No preset — user must provide all fields
		if clientID == "" || issuerURL == "" {
			return "", "", "", nil // no auth configured
		}
		// issuerURL-based discovery would go here for full OIDC support
		return issuerURL + "/authorize", issuerURL + "/token", scopes, nil
	}

	preset := ResolveProviderPreset(provider)
	if preset == nil {
		return "", "", "", nil // unknown provider, skip auth
	}

	resolvedScopes = scopes
	if resolvedScopes == "" {
		resolvedScopes = stringsJoin(preset.DefaultScopes, " ")
	}

	return preset.AuthorizeURL, preset.TokenURL, resolvedScopes, nil
}

func stringsJoin(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
