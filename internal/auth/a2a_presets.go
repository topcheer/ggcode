package auth

import "fmt"

type OAuth2ProviderPreset struct {
	Name            string
	AuthorizeURL    string
	TokenURL        string
	DeviceAuthURL   string
	UserInfoURL     string
	OIDCDiscovery   string
	DefaultScopes   []string
	DefaultClientID string
	SupportsPKCE    bool
	SupportsDevice  bool
}

var ProviderPresets = map[string]OAuth2ProviderPreset{
	"github": {
		Name:            "GitHub",
		AuthorizeURL:    "https://github.com/login/oauth/authorize",
		TokenURL:        "https://github.com/login/oauth/access_token",
		DeviceAuthURL:   "https://github.com/login/device/code",
		DefaultScopes:   []string{"read:user", "user:email"},
		DefaultClientID: "Ov23liq0EQyT4VDz3ayn",
		SupportsPKCE:    true,
		SupportsDevice:  true,
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
		AuthorizeURL:   "https://AUTH0_TENANT.auth0.com/authorize",
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

func ResolveProviderPreset(provider string) *OAuth2ProviderPreset {
	p, ok := ProviderPresets[provider]
	if !ok {
		return nil
	}
	return &p
}

func ResolveA2AAuth(provider, clientID, issuerURL, scopes string) (authorizeURL, tokenURL, resolvedClientID, resolvedScopes string, err error) {
	if provider == "" {
		if clientID == "" || issuerURL == "" {
			return "", "", "", "", nil
		}
		return issuerURL + "/authorize", issuerURL + "/token", clientID, scopes, nil
	}

	preset := ResolveProviderPreset(provider)
	if preset == nil {
		return "", "", "", "", fmt.Errorf("a2a auth: unknown provider %q (known providers: github, google, auth0, azure); fix the provider name or leave it empty and set client_id + issuer_url", provider)
	}

	resolvedScopes = scopes
	if resolvedScopes == "" {
		resolvedScopes = stringsJoin(preset.DefaultScopes, " ")
	}

	resolvedClientID = clientID
	if resolvedClientID == "" {
		resolvedClientID = preset.DefaultClientID
	}

	return preset.AuthorizeURL, preset.TokenURL, resolvedClientID, resolvedScopes, nil
}

func ResolveA2AIssuerURL(provider, issuerURL string) string {
	if issuerURL != "" {
		return issuerURL
	}
	p := ResolveProviderPreset(provider)
	if p == nil {
		return ""
	}
	return p.OIDCDiscovery
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
