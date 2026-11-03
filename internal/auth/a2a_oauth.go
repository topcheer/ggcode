package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

type A2AOAuth2Config struct {
	ClientID     string
	ClientSecret string
	AuthorizeURL string
	TokenURL     string
	Scopes       []string
}

type PKCEToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scope        string    `json:"scope,omitempty"`
}

func StartPKCEFlow(ctx context.Context, cfg A2AOAuth2Config) (*PKCEToken, error) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate verifier: %w", err)
	}
	challenge := GenerateCodeChallenge(verifier)
	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	authURL, _ := url.Parse(cfg.AuthorizeURL)
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	resultCh := make(chan *PKCEToken, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			return
		}
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		token, err := exchangeCodeForToken(ctx, cfg, code, redirectURI, verifier)
		if err != nil {
			errCh <- fmt.Errorf("token exchange: %w", err)
			http.Error(w, "token exchange failed", http.StatusInternalServerError)
			return
		}

		resultCh <- token
		fmt.Fprintf(w, "<html><body><h2>✓ Authentication successful!</h2><p>You can close this tab.</p></body></html>")
	})

	safego.Go("auth.a2aOAuth.serve", func() { _ = srv.Serve(listener) })
	defer srv.Close()

	debug.Log("a2a.oauth", "opening browser for authentication")
	debug.Log("a2a.oauth", "auth URL: %s", authURL.String())
	openBrowser(authURL.String())

	select {
	case token := <-resultCh:
		return token, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("authentication timed out")
	}
}

func exchangeCodeForToken(ctx context.Context, cfg A2AOAuth2Config, code, redirectURI, verifier string) (*PKCEToken, error) {
	reqBody := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     cfg.ClientID,
		"code_verifier": verifier,
	}
	if cfg.ClientSecret != "" {
		reqBody["client_secret"] = cfg.ClientSecret
	} else if secret := os.Getenv("GGCODE_OAUTH_CLIENT_SECRET"); secret != "" {
		reqBody["client_secret"] = secret
	}
	bodyJSON, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST token endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
	contentType := resp.Header.Get("Content-Type")

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var raw map[string]interface{}
	if strings.Contains(contentType, "application/json") {
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("parse token response: %w", err)
		}
	} else {
		if err := json.Unmarshal(body, &raw); err != nil {
			vals, parseErr := url.ParseQuery(string(body))
			if parseErr != nil {
				return nil, fmt.Errorf("parse token response (url-encoded): %w", parseErr)
			}
			raw = make(map[string]interface{})
			for k, v := range vals {
				if len(v) > 0 {
					raw[k] = v[0]
				}
			}
		}
	}

	token := &PKCEToken{
		AccessToken:  strVal(raw["access_token"]),
		RefreshToken: strVal(raw["refresh_token"]),
		TokenType:    strVal(raw["token_type"]),
		Scope:        strVal(raw["scope"]),
	}
	if exp, ok := raw["expires_in"].(float64); ok {
		token.Expiry = time.Now().Add(time.Duration(exp) * time.Second)
	}
	return token, nil
}

func StartDeviceFlow(ctx context.Context, cfg A2AOAuth2Config) (*PKCEToken, error) {
	reqBody := map[string]string{
		"client_id": cfg.ClientID,
		"scope":     strings.Join(cfg.Scopes, " "),
	}
	bodyJSON, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.AuthorizeURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	var deviceResp struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
	}
	respBody, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)

	var errResp struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
		return nil, fmt.Errorf("device code request failed: %s (%s)", errResp.Error, errResp.ErrorDescription)
	}

	if err := json.Unmarshal(respBody, &deviceResp); err != nil {
		return nil, fmt.Errorf("parse device response: %w", err)
	}

	debug.Log("a2a.oauth", "device authentication required")
	debug.Log("a2a.oauth", "device visit: %s", deviceResp.VerificationURI)
	debug.Log("a2a.oauth", "device code: %s", deviceResp.UserCode)

	copyToClipboard(deviceResp.UserCode)
	debug.Log("a2a.oauth", "code copied to clipboard")
	openBrowser(deviceResp.VerificationURI)

	interval := time.Duration(deviceResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)

	for {
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device code expired")
		}

		token, err := pollDeviceToken(ctx, cfg, deviceResp.DeviceCode)
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") {
				continue
			}
			if strings.Contains(err.Error(), "slow_down") {
				interval += 5 * time.Second
				continue
			}
			return nil, err
		}
		return token, nil
	}
}

func pollDeviceToken(ctx context.Context, cfg A2AOAuth2Config, deviceCode string) (*PKCEToken, error) {
	data := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {cfg.ClientID},
		"device_code": {deviceCode},
	}

	bodyJSON, _ := json.Marshal(map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"client_id":   cfg.ClientID,
		"device_code": deviceCode,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
	_ = data
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse device token response: %w", err)
	}

	if errMsg, ok := raw["error"].(string); ok {
		if errDesc, ok := raw["error_description"].(string); ok {
			return nil, fmt.Errorf("%s: %s", errMsg, errDesc)
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	token := &PKCEToken{
		AccessToken:  strVal(raw["access_token"]),
		RefreshToken: strVal(raw["refresh_token"]),
		TokenType:    strVal(raw["token_type"]),
	}
	if exp, ok := raw["expires_in"].(float64); ok {
		token.Expiry = time.Now().Add(time.Duration(exp) * time.Second)
	}
	return token, nil
}

type TokenValidator struct {
	clientID      string
	issuerURL     string
	jwksURL       string
	mu            sync.Mutex
	jwksKeys      map[string]interface{}
	jwksExp       time.Time
	jwksFetchedAt time.Time
	introURL      string
	introExp      time.Time
	validIssuers  []string
	hmacSecret    string
}

type ValidatorOption func(*TokenValidator)

func WithValidIssuers(issuers []string) ValidatorOption {
	return func(tv *TokenValidator) {
		tv.validIssuers = issuers
	}
}

func WithHMACSecret(secret string) ValidatorOption {
	return func(tv *TokenValidator) {
		tv.hmacSecret = secret
	}
}

func NewTokenValidator(clientID, issuerURL string, opts ...ValidatorOption) (*TokenValidator, error) {
	if issuerURL == "" {
		return nil, fmt.Errorf("issuer URL is required for token validation")
	}
	tv := &TokenValidator{
		clientID:  clientID,
		issuerURL: issuerURL,
	}
	for _, opt := range opts {
		opt(tv)
	}
	if strings.Contains(issuerURL, "/.well-known") {
		tv.jwksURL = issuerURL
	} else {
		tv.jwksURL = strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	}
	return tv, nil
}

func (v *TokenValidator) ValidateToken(ctx context.Context, token string) (map[string]interface{}, error) {
	if parts := strings.Split(token, "."); len(parts) == 3 {
		return v.validateJWT(ctx, token)
	}

	return v.validateOpaqueToken(ctx, token)
}

func (v *TokenValidator) validateJWT(ctx context.Context, tokenString string) (map[string]interface{}, error) {
	unverifiedToken, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("parse JWT: %w", err)
	}

	kid := ""
	if kidVal, ok := unverifiedToken.Header["kid"]; ok {
		kid = fmt.Sprintf("%v", kidVal)
	}

	alg, _ := unverifiedToken.Header["alg"].(string)

	keyFunc := func(token *jwt.Token) (interface{}, error) {
		switch alg {
		case "RS256", "RS384", "RS512":
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return v.getPublicKey(ctx, kid)
		case "ES256", "ES384", "ES512":
			if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return v.getPublicKey(ctx, kid)
		case "HS256", "HS384", "HS512":
			if v.hmacSecret == "" {
				return nil, fmt.Errorf("HMAC token rejected: no hmac_secret configured (clientID must not be used as HMAC key)")
			}
			return []byte(v.hmacSecret), nil
		default:
			return nil, fmt.Errorf("unsupported signing method: %s", alg)
		}
	}

	token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, keyFunc,
		jwt.WithAudience(v.clientID),
	)
	if err != nil {
		return nil, fmt.Errorf("validate JWT: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid JWT claims")
	}

	iss, ok := claims["iss"].(string)
	if !ok {
		return nil, fmt.Errorf("token missing iss claim")
	}
	if !v.isIssuerAllowed(iss) {
		return nil, fmt.Errorf("token issuer %q not in allowed list", iss)
	}

	exp, hasExp := claims["exp"]
	if !hasExp {
		return nil, fmt.Errorf("token missing exp claim")
	}
	expFloat, ok := exp.(float64)
	if !ok {
		return nil, fmt.Errorf("token exp claim has invalid type: %T", exp)
	}
	if time.Unix(int64(expFloat), 0).Before(time.Now()) {
		return nil, fmt.Errorf("token expired at %v", exp)
	}

	return map[string]interface{}(claims), nil
}

func (v *TokenValidator) isIssuerAllowed(iss string) bool {
	v.mu.Lock()
	issuer := v.issuerURL
	v.mu.Unlock()
	candidates := []string{issuer}
	candidates = append(candidates, v.validIssuers...)
	for _, candidate := range candidates {
		if iss == candidate {
			return true
		}
		if strings.TrimRight(iss, "/") == strings.TrimRight(candidate, "/") {
			return true
		}
		if idx := strings.Index(candidate, "/.well-known/"); idx >= 0 {
			baseURL := candidate[:idx]
			if strings.TrimRight(iss, "/") == strings.TrimRight(baseURL, "/") {
				return true
			}
		}
	}
	return false
}

func (v *TokenValidator) getPublicKey(ctx context.Context, kid string) (interface{}, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.jwksKeys != nil && len(v.jwksKeys) > 0 && time.Now().Before(v.jwksExp) {
		if key, ok := v.jwksKeys[kid]; ok {
			return key, nil
		}
	}

	if err := v.refreshJWKS(ctx); err != nil {
		if key, ok := v.staleKeyLocked(kid); ok {
			debug.Log("auth", "JWKS refresh failed (%v); serving stale key set fetched %s ago",
				err, time.Since(v.jwksFetchedAt).Round(time.Second))
			return key, nil
		}
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}

	if key, ok := v.jwksKeys[kid]; ok {
		return key, nil
	}

	return nil, fmt.Errorf("key ID %q not found in JWKS", kid)
}

const maxJWKSStaleAge = 72 * time.Hour

func (v *TokenValidator) staleKeyLocked(kid string) (interface{}, bool) {
	if len(v.jwksKeys) == 0 || v.jwksFetchedAt.IsZero() {
		return nil, false
	}
	if time.Since(v.jwksFetchedAt) > maxJWKSStaleAge {
		return nil, false
	}
	key, ok := v.jwksKeys[kid]
	return key, ok
}

func (v *TokenValidator) refreshJWKS(ctx context.Context) error {
	discoveryURL := v.jwksURL

	var jwksURL string
	if strings.HasSuffix(discoveryURL, "/.well-known/openid-configuration") ||
		strings.HasSuffix(discoveryURL, ".well-known/openid-configuration") {
		req, err := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("OIDC discovery: %w", err)
		}
		defer resp.Body.Close()

		var discovery struct {
			JWKSURI string `json:"jwks_uri"`
			Issuer  string `json:"issuer"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
			return fmt.Errorf("parse discovery: %w", err)
		}
		jwksURL = discovery.JWKSURI
		if discovery.Issuer != "" {
			v.issuerURL = discovery.Issuer
		}
	} else {
		jwksURL = discoveryURL
	}

	if jwksURL == "" {
		return fmt.Errorf("no JWKS URL found")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", jwksURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			X   string `json:"x"`
			Y   string `json:"y"`
			Crv string `json:"crv"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}

	keys := make(map[string]interface{})
	for _, key := range jwks.Keys {
		parsedKey, err := v.parseJWK(key)
		if err != nil {
			continue
		}
		keys[key.Kid] = parsedKey
	}

	if len(keys) == 0 {
		return fmt.Errorf("no valid keys in JWKS")
	}

	v.jwksKeys = keys
	v.jwksExp = time.Now().Add(1 * time.Hour)
	v.jwksFetchedAt = time.Now()
	return nil
}

func (v *TokenValidator) parseJWK(jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Crv string `json:"crv"`
}) (interface{}, error) {
	switch jwk.Kty {
	case "RSA":
		n, err := base64URLDecode(jwk.N)
		if err != nil {
			return nil, fmt.Errorf("decode n: %w", err)
		}
		e, err := base64URLDecode(jwk.E)
		if err != nil {
			return nil, fmt.Errorf("decode e: %w", err)
		}
		exp := 0
		for _, b := range e {
			exp = exp*256 + int(b)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: exp,
		}, nil
	case "EC":
		x, err := base64URLDecode(jwk.X)
		if err != nil {
			return nil, fmt.Errorf("decode x: %w", err)
		}
		y, err := base64URLDecode(jwk.Y)
		if err != nil {
			return nil, fmt.Errorf("decode y: %w", err)
		}
		var curve elliptic.Curve
		switch jwk.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve: %s", jwk.Crv)
		}
		return &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(x),
			Y:     new(big.Int).SetBytes(y),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported key type: %s", jwk.Kty)
	}
}

func (v *TokenValidator) validateOpaqueToken(ctx context.Context, token string) (map[string]interface{}, error) {
	v.mu.Lock()
	introspectURL := v.issuerURL
	v.mu.Unlock()

	if ep := v.discoverIntrospectionEndpoint(ctx); ep != "" {
		introspectURL = ep
	} else if strings.HasSuffix(introspectURL, "/token") {
		introspectURL = strings.Replace(introspectURL, "/token", "/introspect", 1)
	} else {
		introspectURL = strings.TrimRight(introspectURL, "/") + "/introspect"
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", introspectURL, strings.NewReader(
		"token="+url.QueryEscape(token),
	))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse introspect response: %w", err)
	}

	if active, _ := result["active"].(bool); !active {
		return nil, fmt.Errorf("token is not active")
	}
	return result, nil
}

func (v *TokenValidator) discoverIntrospectionEndpoint(ctx context.Context) string {
	v.mu.Lock()
	if v.introURL != "" && time.Now().Before(v.introExp) {
		ep := v.introURL
		v.mu.Unlock()
		return ep
	}
	discoveryURL := v.jwksURL
	v.mu.Unlock()

	if discoveryURL == "" || !strings.Contains(discoveryURL, "/.well-known/") {
		return ""
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var doc struct {
		IntrospectionEndpoint string `json:"introspection_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil || doc.IntrospectionEndpoint == "" {
		return ""
	}

	v.mu.Lock()
	v.introURL = doc.IntrospectionEndpoint
	v.introExp = time.Now().Add(5 * time.Minute)
	v.mu.Unlock()
	return doc.IntrospectionEndpoint
}

func base64URLDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

type MTLSConfig struct {
	CertFile string
	KeyFile  string
	CAFile   string
}

func (c *MTLSConfig) BuildTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load cert/key: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA cert")
		}
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func strVal(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}

func copyToClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy", text)
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else {
			return
		}
	case "windows":
		cmd = exec.Command("clip")
	default:
		return
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	stdin.Write([]byte(text))
	stdin.Close()
	_ = cmd.Wait()
}

type PKCETokenProvider struct {
	Config   A2AOAuth2Config
	Provider string
	Cache    *TokenCache
}

func (p *PKCETokenProvider) GetToken(ctx context.Context) (string, string, time.Time, error) {
	cacheKey := CacheKey(p.Provider, p.Config.ClientID)

	if p.Cache != nil && cacheKey != "" {
		if cached := p.Cache.LoadValid(cacheKey); cached != nil {
			return cached.AccessToken, cached.RefreshToken, cached.Expiry, nil
		}
	}

	token, err := StartPKCEFlow(ctx, p.Config)
	if err != nil {
		return "", "", time.Time{}, err
	}

	if p.Cache != nil && cacheKey != "" {
		_ = p.Cache.Save(cacheKey, token, p.Config.ClientID)
	}

	return token.AccessToken, token.RefreshToken, token.Expiry, nil
}

type DeviceFlowTokenProvider struct {
	Config   A2AOAuth2Config
	Provider string
	Cache    *TokenCache
}

func (p *DeviceFlowTokenProvider) GetToken(ctx context.Context) (string, string, time.Time, error) {
	cacheKey := CacheKey(p.Provider, p.Config.ClientID)

	if p.Cache != nil && cacheKey != "" {
		if cached := p.Cache.LoadValid(cacheKey); cached != nil {
			return cached.AccessToken, cached.RefreshToken, cached.Expiry, nil
		}
	}

	token, err := StartDeviceFlow(ctx, p.Config)
	if err != nil {
		return "", "", time.Time{}, err
	}

	if p.Cache != nil && cacheKey != "" {
		_ = p.Cache.Save(cacheKey, token, p.Config.ClientID)
	}

	return token.AccessToken, token.RefreshToken, token.Expiry, nil
}

func NewTokenProviderFromPreset(provider string, clientSecret string, headless bool) (interface {
	GetToken(ctx context.Context) (string, string, time.Time, error)
}, error) {
	preset := ResolveProviderPreset(provider)
	if preset == nil {
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}

	cfg := A2AOAuth2Config{
		ClientID:     preset.DefaultClientID,
		ClientSecret: clientSecret,
		TokenURL:     preset.TokenURL,
		Scopes:       preset.DefaultScopes,
	}

	cache := NewTokenCache(DefaultTokenCacheDir())

	if headless || !preset.SupportsPKCE {
		if !preset.SupportsDevice {
			return nil, fmt.Errorf("provider %s does not support device flow", provider)
		}
		cfg.AuthorizeURL = preset.DeviceAuthURL
		return &DeviceFlowTokenProvider{Config: cfg, Provider: provider, Cache: cache}, nil
	}

	cfg.AuthorizeURL = preset.AuthorizeURL
	return &PKCETokenProvider{Config: cfg, Provider: provider, Cache: cache}, nil
}
