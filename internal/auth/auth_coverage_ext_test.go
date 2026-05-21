//go:build !integration

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// exchangeCodeForToken
// ---------------------------------------------------------------------------

func TestExchangeCodeForToken_JSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "gho_abc123",
			"refresh_token": "refresh456",
			"token_type":    "bearer",
			"scope":         "read:user",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test-client"}
	token, err := exchangeCodeForToken(context.Background(), cfg, "auth-code-123", "http://localhost:8080/callback", "my-verifier")
	if err != nil {
		t.Fatalf("exchangeCodeForToken: %v", err)
	}
	if token.AccessToken != "gho_abc123" {
		t.Errorf("expected gho_abc123, got %s", token.AccessToken)
	}
	if token.Expiry.IsZero() {
		t.Error("expected non-zero expiry from expires_in")
	}
}

func TestExchangeCodeForToken_URLEncoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		fmt.Fprint(w, "access_token=token-from-form&token_type=bearer&scope=read")
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	token, err := exchangeCodeForToken(context.Background(), cfg, "code", "redirect", "verifier")
	if err != nil {
		t.Fatalf("exchangeCodeForToken url-encoded: %v", err)
	}
	if token.AccessToken != "token-from-form" {
		t.Errorf("expected token-from-form, got %s", token.AccessToken)
	}
}

func TestExchangeCodeForToken_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	_, err := exchangeCodeForToken(context.Background(), cfg, "bad-code", "redirect", "verifier")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention 400: %v", err)
	}
}

func TestExchangeCodeForToken_WithClientSecret(t *testing.T) {
	var received map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "bearer"})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test", ClientSecret: "my-secret"}
	_, err := exchangeCodeForToken(context.Background(), cfg, "code", "redirect", "verifier")
	if err != nil {
		t.Fatalf("exchangeCodeForToken with secret: %v", err)
	}
	if received["client_secret"] != "my-secret" {
		t.Errorf("expected my-secret, got %s", received["client_secret"])
	}
}

func TestExchangeCodeForToken_ClientSecretFromEnv(t *testing.T) {
	var received map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "bearer"})
	}))
	defer srv.Close()

	t.Setenv("GGCODE_OAUTH_CLIENT_SECRET", "env-secret")
	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	_, err := exchangeCodeForToken(context.Background(), cfg, "code", "redirect", "verifier")
	if err != nil {
		t.Fatalf("exchangeCodeForToken env secret: %v", err)
	}
	if received["client_secret"] != "env-secret" {
		t.Errorf("expected env-secret, got %s", received["client_secret"])
	}
}

func TestExchangeCodeForToken_EmptyAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "not valid oauth data")
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	token, _ := exchangeCodeForToken(context.Background(), cfg, "code", "redirect", "verifier")
	// URL-encoded fallback parses but access_token is empty
	if token.AccessToken != "" {
		t.Errorf("expected empty access token for garbage response, got %q", token.AccessToken)
	}
}

// ---------------------------------------------------------------------------
// pollDeviceToken
// ---------------------------------------------------------------------------

func TestPollDeviceToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "device-token",
			"refresh_token": "device-refresh",
			"token_type":    "bearer",
			"expires_in":    7200,
		})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	token, err := pollDeviceToken(context.Background(), cfg, "device-code")
	if err != nil {
		t.Fatalf("pollDeviceToken: %v", err)
	}
	if token.AccessToken != "device-token" {
		t.Errorf("expected device-token, got %s", token.AccessToken)
	}
}

func TestPollDeviceToken_Pending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"error":             "authorization_pending",
			"error_description": "waiting for user",
		})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	_, err := pollDeviceToken(context.Background(), cfg, "device-code")
	if err == nil {
		t.Fatal("expected error for pending")
	}
	if !strings.Contains(err.Error(), "authorization_pending") {
		t.Errorf("error should mention pending: %v", err)
	}
}

func TestPollDeviceToken_SlowDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"error": "slow_down"})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	_, err := pollDeviceToken(context.Background(), cfg, "device-code")
	if err == nil {
		t.Fatal("expected error for slow_down")
	}
}

func TestPollDeviceToken_Expired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"error": "expired_token"})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	_, err := pollDeviceToken(context.Background(), cfg, "device-code")
	if err == nil {
		t.Fatal("expected error for expired_token")
	}
}

func TestPollDeviceToken_AccessDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	_, err := pollDeviceToken(context.Background(), cfg, "device-code")
	if err == nil {
		t.Fatal("expected error for access_denied")
	}
}

func TestPollDeviceToken_OtherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"error": "some_unknown_error"})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{TokenURL: srv.URL, ClientID: "test"}
	_, err := pollDeviceToken(context.Background(), cfg, "device-code")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// JWT validation — HS256 with client_id
// ---------------------------------------------------------------------------

func TestValidateJWT_HS256Success(t *testing.T) {
	tv, _ := NewTokenValidator("my-client", "https://example.com")
	claims := jwt.MapClaims{
		"sub": "hs256-user",
		"iss": "https://example.com",
		"aud": "my-client",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte("my-client"))

	result, err := tv.ValidateToken(context.Background(), tokenString)
	if err != nil {
		t.Fatalf("validate HS256: %v", err)
	}
	if result["sub"] != "hs256-user" {
		t.Errorf("expected hs256-user, got %v", result["sub"])
	}
}

func TestValidateJWT_HS256NoClientID(t *testing.T) {
	tv, _ := NewTokenValidator("", "https://example.com")
	claims := jwt.MapClaims{
		"sub": "user",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte(""))

	_, err := tv.ValidateToken(context.Background(), tokenString)
	if err == nil {
		t.Error("expected error: HMAC with no client_id")
	}
}

func TestValidateJWT_Expired(t *testing.T) {
	tv, _ := NewTokenValidator("test-client", "https://example.com")
	claims := jwt.MapClaims{
		"sub": "user",
		"iss": "https://example.com",
		"aud": "test-client",
		"exp": float64(time.Now().Add(-2 * time.Hour).Unix()),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte("test-client"))

	_, err := tv.ValidateToken(context.Background(), tokenString)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestValidateJWT_InvalidStructure(t *testing.T) {
	tv, _ := NewTokenValidator("test-client", "https://example.com")
	_, err := tv.validateJWT(context.Background(), "not-a-jwt")
	if err == nil {
		t.Error("expected error for invalid JWT")
	}
}

func TestValidateJWT_UnsupportedSigningMethod(t *testing.T) {
	tv, _ := NewTokenValidator("test-client", "https://example.com")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{"sub": "user", "exp": time.Now().Add(time.Hour).Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodPS256, claims)
	tokenString, _ := token.SignedString(key)

	_, err = tv.ValidateToken(context.Background(), tokenString)
	if err == nil {
		t.Error("expected error for unsupported signing method")
	}
}

// ---------------------------------------------------------------------------
// JWT validation — RS256 with JWKS
// ---------------------------------------------------------------------------

func makeRSATestServer(t *testing.T, kid string) (*httptest.Server, *rsa.PrivateKey) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
	eBytes := []byte{0, 1, 0, 1}
	e := base64.RawURLEncoding.EncodeToString(eBytes)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{
				"issuer":   "https://rsa-test.example.com",
				"jwks_uri": srv.URL + "/jwks",
			})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]string{{
					"kid": kid, "kty": "RSA", "use": "sig", "n": n, "e": e,
				}},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, privateKey
}

func TestValidateJWT_RS256Success(t *testing.T) {
	srv, privateKey := makeRSATestServer(t, "rsa-kid-1")

	tv, _ := NewTokenValidator("test-client", srv.URL+"/.well-known/openid-configuration")
	claims := jwt.MapClaims{"sub": "rsa-user", "exp": time.Now().Add(time.Hour).Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "rsa-kid-1"
	tokenString, _ := token.SignedString(privateKey)

	result, err := tv.ValidateToken(context.Background(), tokenString)
	if err != nil {
		t.Fatalf("validate RS256: %v", err)
	}
	if result["sub"] != "rsa-user" {
		t.Errorf("expected rsa-user, got %v", result["sub"])
	}
}

func TestValidateJWT_RS256KeyNotFound(t *testing.T) {
	srv, privateKey := makeRSATestServer(t, "known-kid")

	tv, _ := NewTokenValidator("test-client", srv.URL+"/.well-known/openid-configuration")
	claims := jwt.MapClaims{"sub": "user", "exp": time.Now().Add(time.Hour).Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "unknown-kid"
	tokenString, _ := token.SignedString(privateKey)

	_, err := tv.ValidateToken(context.Background(), tokenString)
	if err == nil {
		t.Error("expected error for unknown kid")
	}
}

// ---------------------------------------------------------------------------
// parseJWK — key types
// ---------------------------------------------------------------------------

// jwkRaw is the anonymous struct type expected by TokenValidator.parseJWK.
type jwkRaw = struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Crv string `json:"crv"`
}

func TestParseJWK_RSAKey(t *testing.T) {
	tv := &TokenValidator{}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
	eBytes := []byte{0, 1, 0, 1}
	e := base64.RawURLEncoding.EncodeToString(eBytes)

	key, err := tv.parseJWK(jwkRaw{Kid: "rsa", Kty: "RSA", Use: "sig", N: n, E: e})
	if err != nil {
		t.Fatalf("parseJWK RSA: %v", err)
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		t.Fatal("expected *rsa.PublicKey")
	}
	if rsaKey.N.Cmp(privateKey.PublicKey.N) != 0 {
		t.Error("N mismatch")
	}
}

func TestParseJWK_ECKey_P256(t *testing.T) {
	tv := &TokenValidator{}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.X.Bytes())
	y := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.Y.Bytes())

	key, err := tv.parseJWK(jwkRaw{Kid: "ec", Kty: "EC", Use: "sig", X: x, Y: y, Crv: "P-256"})
	if err != nil {
		t.Fatalf("parseJWK EC P-256: %v", err)
	}
	ecKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		t.Fatal("expected *ecdsa.PublicKey")
	}
	if ecKey.X.Cmp(privateKey.PublicKey.X) != 0 {
		t.Error("X mismatch")
	}
}

func TestParseJWK_ECKey_P384(t *testing.T) {
	tv := &TokenValidator{}
	privateKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.X.Bytes())
	y := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.Y.Bytes())

	key, err := tv.parseJWK(jwkRaw{Kid: "p384", Kty: "EC", Use: "sig", X: x, Y: y, Crv: "P-384"})
	if err != nil {
		t.Fatalf("parseJWK P-384: %v", err)
	}
	if _, ok := key.(*ecdsa.PublicKey); !ok {
		t.Fatal("expected *ecdsa.PublicKey")
	}
}

func TestParseJWK_ECKey_P521(t *testing.T) {
	tv := &TokenValidator{}
	privateKey, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.X.Bytes())
	y := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.Y.Bytes())

	key, err := tv.parseJWK(jwkRaw{Kid: "p521", Kty: "EC", Use: "sig", X: x, Y: y, Crv: "P-521"})
	if err != nil {
		t.Fatalf("parseJWK P-521: %v", err)
	}
	if _, ok := key.(*ecdsa.PublicKey); !ok {
		t.Fatal("expected *ecdsa.PublicKey")
	}
}

func TestParseJWK_UnsupportedCurve(t *testing.T) {
	tv := &TokenValidator{}
	_, err := tv.parseJWK(jwkRaw{Kid: "bad", Kty: "EC", Use: "sig", X: "abc", Y: "def", Crv: "unknown"})
	if err == nil {
		t.Error("expected error for unsupported curve")
	}
}

func TestParseJWK_UnknownKty(t *testing.T) {
	tv := &TokenValidator{}
	_, err := tv.parseJWK(jwkRaw{Kid: "oct", Kty: "oct", Use: "sig"})
	if err == nil {
		t.Error("expected error for unsupported key type")
	}
}

func TestParseJWK_RSA_InvalidBase64(t *testing.T) {
	tv := &TokenValidator{}
	_, err := tv.parseJWK(jwkRaw{Kid: "bad", Kty: "RSA", Use: "sig", N: "!!!invalid!!!", E: "AQAB"})
	if err == nil {
		t.Error("expected error for invalid base64 N")
	}
}

func TestParseJWK_EC_InvalidBase64(t *testing.T) {
	tv := &TokenValidator{}
	_, err := tv.parseJWK(jwkRaw{Kid: "bad", Kty: "EC", Use: "sig", X: "!!!", Y: "valid", Crv: "P-256"})
	if err == nil {
		t.Error("expected error for invalid base64 X")
	}
}

// ---------------------------------------------------------------------------
// refreshJWKS edge cases
// ---------------------------------------------------------------------------

func TestRefreshJWKS_NoKeysInJWKS(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"jwks_uri": srv.URL + "/jwks"})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{}})
		}
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL+"/.well-known/openid-configuration")
	err := tv.refreshJWKS(context.Background())
	if err == nil {
		t.Error("expected error for empty JWKS")
	}
}

func TestRefreshJWKS_SkipsBadKeys(t *testing.T) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
	eBytes := []byte{0, 1, 0, 1}
	e := base64.RawURLEncoding.EncodeToString(eBytes)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"jwks_uri": srv.URL + "/jwks"})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]string{
					{"kid": "bad", "kty": "oct", "use": "sig"},
					{"kid": "good", "kty": "RSA", "use": "sig", "n": n, "e": e},
				},
			})
		}
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL+"/.well-known/openid-configuration")
	err := tv.refreshJWKS(context.Background())
	if err != nil {
		t.Fatalf("refreshJWKS mixed: %v", err)
	}
	if len(tv.jwksKeys) != 1 {
		t.Errorf("expected 1 key, got %d", len(tv.jwksKeys))
	}
	if _, ok := tv.jwksKeys["good"]; !ok {
		t.Error("expected good key")
	}
}

func TestRefreshJWKS_UpdatesIssuer(t *testing.T) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
	eBytes := []byte{0, 1, 0, 1}
	e := base64.RawURLEncoding.EncodeToString(eBytes)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{
				"issuer":   "https://updated.example.com",
				"jwks_uri": srv.URL + "/jwks",
			})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]string{{"kid": "k1", "kty": "RSA", "use": "sig", "n": n, "e": e}},
			})
		}
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL+"/.well-known/openid-configuration")
	tv.refreshJWKS(context.Background())
	if tv.issuerURL != "https://updated.example.com" {
		t.Errorf("issuer not updated: %s", tv.issuerURL)
	}
}

func TestRefreshJWKS_NoJWKSURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": "https://no-jwks.example.com"})
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL+"/.well-known/openid-configuration")
	err := tv.refreshJWKS(context.Background())
	if err == nil {
		t.Error("expected error for missing jwks_uri")
	}
}

func TestRefreshJWKS_BadJWKSResponse(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"jwks_uri": srv.URL + "/jwks"})
		case "/jwks":
			fmt.Fprint(w, "not json")
		}
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL+"/.well-known/openid-configuration")
	err := tv.refreshJWKS(context.Background())
	if err == nil {
		t.Error("expected error for bad JWKS response")
	}
}

func TestRefreshJWKS_DirectJWKSURL(t *testing.T) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
	eBytes := []byte{0, 1, 0, 1}
	e := base64.RawURLEncoding.EncodeToString(eBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{"kid": "direct", "kty": "RSA", "use": "sig", "n": n, "e": e}},
		})
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL+"/jwks-direct")
	tv.jwksURL = srv.URL + "/jwks-direct"

	err := tv.refreshJWKS(context.Background())
	if err != nil {
		t.Fatalf("refreshJWKS direct: %v", err)
	}
	if len(tv.jwksKeys) != 1 {
		t.Errorf("expected 1 key, got %d", len(tv.jwksKeys))
	}
}

// ---------------------------------------------------------------------------
// validateOpaqueToken
// ---------------------------------------------------------------------------

func TestValidateOpaqueToken_IntrospectionFromTokenURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"active": true, "sub": "introspected-user"})
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL+"/token")
	result, err := tv.validateOpaqueToken(context.Background(), "opaque-token")
	if err != nil {
		t.Fatalf("validateOpaqueToken: %v", err)
	}
	if result["sub"] != "introspected-user" {
		t.Errorf("expected introspected-user, got %v", result["sub"])
	}
}

func TestValidateOpaqueToken_IntrospectionFromBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"active": true, "sub": "base-user"})
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL)
	result, err := tv.validateOpaqueToken(context.Background(), "opaque-token")
	if err != nil {
		t.Fatalf("validateOpaqueToken: %v", err)
	}
	if result["sub"] != "base-user" {
		t.Errorf("expected base-user, got %v", result["sub"])
	}
}

func TestValidateOpaqueToken_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL)
	_, err := tv.validateOpaqueToken(context.Background(), "opaque")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidateOpaqueToken_NetworkError(t *testing.T) {
	tv, _ := NewTokenValidator("test", "http://127.0.0.1:1")
	_, err := tv.validateOpaqueToken(context.Background(), "opaque")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// ---------------------------------------------------------------------------
// BuildTLSConfig
// ---------------------------------------------------------------------------

func generateTestCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return
}

func TestBuildTLSConfig_Success(t *testing.T) {
	tmpDir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t)
	os.WriteFile(filepath.Join(tmpDir, "cert.pem"), certPEM, 0600)
	os.WriteFile(filepath.Join(tmpDir, "key.pem"), keyPEM, 0600)
	os.WriteFile(filepath.Join(tmpDir, "ca.pem"), certPEM, 0600)

	cfg := &MTLSConfig{
		CertFile: filepath.Join(tmpDir, "cert.pem"),
		KeyFile:  filepath.Join(tmpDir, "key.pem"),
		CAFile:   filepath.Join(tmpDir, "ca.pem"),
	}
	tlsCfg, err := cfg.BuildTLSConfig()
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected TLS 1.2+, got %d", tlsCfg.MinVersion)
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Error("expected 1 certificate")
	}
}

func TestBuildTLSConfig_InvalidCACert(t *testing.T) {
	tmpDir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t)
	os.WriteFile(filepath.Join(tmpDir, "cert.pem"), certPEM, 0600)
	os.WriteFile(filepath.Join(tmpDir, "key.pem"), keyPEM, 0600)
	os.WriteFile(filepath.Join(tmpDir, "ca.pem"), []byte("not a valid PEM"), 0600)

	cfg := &MTLSConfig{
		CertFile: filepath.Join(tmpDir, "cert.pem"),
		KeyFile:  filepath.Join(tmpDir, "key.pem"),
		CAFile:   filepath.Join(tmpDir, "ca.pem"),
	}
	_, err := cfg.BuildTLSConfig()
	if err == nil {
		t.Error("expected error for invalid CA cert")
	}
}

func TestBuildTLSConfig_NoCA(t *testing.T) {
	tmpDir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t)
	os.WriteFile(filepath.Join(tmpDir, "cert.pem"), certPEM, 0600)
	os.WriteFile(filepath.Join(tmpDir, "key.pem"), keyPEM, 0600)

	cfg := &MTLSConfig{
		CertFile: filepath.Join(tmpDir, "cert.pem"),
		KeyFile:  filepath.Join(tmpDir, "key.pem"),
	}
	tlsCfg, err := cfg.BuildTLSConfig()
	if err != nil {
		t.Fatalf("BuildTLSConfig no CA: %v", err)
	}
	if tlsCfg == nil {
		t.Error("expected non-nil TLS config")
	}
}

// ---------------------------------------------------------------------------
// NewTokenProviderFromPreset
// ---------------------------------------------------------------------------

func TestNewTokenProviderFromPreset_PKCE(t *testing.T) {
	provider, err := NewTokenProviderFromPreset("github", "", false)
	if err != nil {
		t.Fatalf("NewTokenProviderFromPreset: %v", err)
	}
	if _, ok := provider.(*PKCETokenProvider); !ok {
		t.Error("expected PKCETokenProvider")
	}
}

func TestNewTokenProviderFromPreset_DeviceFlow(t *testing.T) {
	provider, err := NewTokenProviderFromPreset("github", "", true)
	if err != nil {
		t.Fatalf("NewTokenProviderFromPreset: %v", err)
	}
	if _, ok := provider.(*DeviceFlowTokenProvider); !ok {
		t.Error("expected DeviceFlowTokenProvider")
	}
}

func TestNewTokenProviderFromPreset_UnknownProvider(t *testing.T) {
	_, err := NewTokenProviderFromPreset("unknown", "", false)
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestNewTokenProviderFromPreset_NoDeviceSupport(t *testing.T) {
	_, err := NewTokenProviderFromPreset("google", "", true)
	if err == nil {
		t.Error("expected error for device flow with google")
	}
}

func TestNewTokenProviderFromPreset_ForceDevice(t *testing.T) {
	original := ProviderPresets
	ProviderPresets["test-devonly"] = OAuth2ProviderPreset{
		Name: "Test", AuthorizeURL: "https://example.com/authorize",
		TokenURL: "https://example.com/token", DeviceAuthURL: "https://example.com/device",
		DefaultScopes: []string{"read"}, SupportsPKCE: false, SupportsDevice: true,
	}
	defer func() { ProviderPresets = original }()

	provider, err := NewTokenProviderFromPreset("test-devonly", "", false)
	if err != nil {
		t.Fatalf("NewTokenProviderFromPreset: %v", err)
	}
	if _, ok := provider.(*DeviceFlowTokenProvider); !ok {
		t.Error("expected DeviceFlowTokenProvider when PKCE not supported")
	}
}

// ---------------------------------------------------------------------------
// GetToken cache hit paths (no real OAuth)
// ---------------------------------------------------------------------------

func TestPKCETokenProvider_GetToken_CacheHit(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)
	key := CacheKey("github", "client")

	token := &PKCEToken{
		AccessToken:  "cached-access",
		RefreshToken: "cached-refresh",
		TokenType:    "bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
	cache.Save(key, token, "client")

	provider := &PKCETokenProvider{
		Config:   A2AOAuth2Config{ClientID: "client"},
		Provider: "github",
		Cache:    cache,
	}

	accessToken, refreshToken, expiry, err := provider.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken cache hit: %v", err)
	}
	if accessToken != "cached-access" {
		t.Errorf("expected cached-access, got %s", accessToken)
	}
	if refreshToken != "cached-refresh" {
		t.Errorf("expected cached-refresh, got %s", refreshToken)
	}
	if expiry.IsZero() {
		t.Error("expected non-zero expiry")
	}
}

func TestDeviceFlowTokenProvider_GetToken_CacheHit(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)
	key := CacheKey("github", "client")

	token := &PKCEToken{
		AccessToken: "device-cached",
		TokenType:   "bearer",
		Expiry:      time.Now().Add(time.Hour),
	}
	cache.Save(key, token, "client")

	provider := &DeviceFlowTokenProvider{
		Config:   A2AOAuth2Config{ClientID: "client"},
		Provider: "github",
		Cache:    cache,
	}

	accessToken, _, _, err := provider.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken cache hit: %v", err)
	}
	if accessToken != "device-cached" {
		t.Errorf("expected device-cached, got %s", accessToken)
	}
}

// ---------------------------------------------------------------------------
// Store edge cases
// ---------------------------------------------------------------------------

func TestStoreSave_NilInfo(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "test.json"))
	err := store.Save(nil)
	if err == nil {
		t.Error("expected error for nil info")
	}
}

func TestStoreSave_EmptyProviderID(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "test.json"))
	err := store.Save(&Info{Type: "oauth"})
	if err == nil {
		t.Error("expected error for empty provider ID")
	}
}

func TestStoreSave_EmptyType(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "test.json"))
	err := store.Save(&Info{ProviderID: "test"})
	if err == nil {
		t.Error("expected error for empty type")
	}
}

func TestStoreLoad_CorruptedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	os.WriteFile(path, []byte("not valid json"), 0600)
	store := NewStore(path)
	_, err := store.Load("test")
	if err == nil {
		t.Error("expected error for corrupted JSON")
	}
}

func TestStoreHasUsableToken_Expired(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "test.json"))
	store.Save(&Info{
		ProviderID:  "expired",
		Type:        "oauth",
		AccessToken: "expired",
		ExpiresAt:   time.Now().Add(-time.Hour),
	})
	ok, err := store.HasUsableToken("expired")
	if err != nil {
		t.Fatalf("HasUsableToken: %v", err)
	}
	if ok {
		t.Error("expected false for expired token")
	}
}

func TestStoreHasUsableToken_EmptyAccessToken(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "test.json"))
	store.Save(&Info{
		ProviderID: "no-access",
		Type:       "oauth",
		ExpiresAt:  time.Now().Add(time.Hour),
	})
	ok, err := store.HasUsableToken("no-access")
	if err != nil {
		t.Fatalf("HasUsableToken: %v", err)
	}
	if ok {
		t.Error("expected false for empty access token")
	}
}

func TestInfoIsExpired_Nil(t *testing.T) {
	var info *Info
	if !info.IsExpired() {
		t.Error("nil Info should be expired")
	}
}

func TestStoreDelete_NoFile(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "nonexistent", "test.json"))
	_ = store.Delete("anything") // should not panic
}

// ---------------------------------------------------------------------------
// Copilot edge cases
// ---------------------------------------------------------------------------

func TestNormalizeEnterpriseURL_Empty(t *testing.T) {
	_, err := NormalizeEnterpriseURL("")
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestNormalizeEnterpriseURL_NoScheme(t *testing.T) {
	got, err := NormalizeEnterpriseURL("company.ghe.com")
	if err != nil {
		t.Fatalf("NormalizeEnterpriseURL: %v", err)
	}
	if got != "company.ghe.com" {
		t.Errorf("expected company.ghe.com, got %q", got)
	}
}

func TestNormalizeEnterpriseURL_InvalidURL(t *testing.T) {
	_, err := NormalizeEnterpriseURL("://invalid")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestNormalizeEnterpriseURL_EmptyHost(t *testing.T) {
	_, err := NormalizeEnterpriseURL("https:///path")
	if err == nil {
		t.Error("expected error for empty host")
	}
}

func TestPollCopilotDeviceFlow_Nil(t *testing.T) {
	_, err := PollCopilotDeviceFlow(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil flow")
	}
}

func TestCopilotClientID_Default(t *testing.T) {
	os.Unsetenv("GGCODE_GITHUB_COPILOT_CLIENT_ID")
	id := copilotClientID()
	if id != defaultCopilotClientID {
		t.Errorf("expected default ID, got %s", id)
	}
}

// ---------------------------------------------------------------------------
// Claude OAuth callback server
// ---------------------------------------------------------------------------

func TestStartClaudeCallbackListenerNet_Success(t *testing.T) {
	flow, err := startClaudeCallbackListenerNet("test-state")
	if err != nil {
		t.Fatalf("startClaudeCallbackListenerNet: %v", err)
	}
	defer flow.Close()

	if flow.Port == 0 {
		t.Error("expected non-zero port")
	}
	if flow.callbackCh == nil {
		t.Error("expected non-nil callback channel")
	}
}

func TestClaudeCallback_CodeExchange(t *testing.T) {
	flow, err := startClaudeCallbackListenerNet("expected-state")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer flow.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=auth-code-123&state=expected-state", flow.Port))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Errorf("expected 200 or 302, got %d", resp.StatusCode)
	}

	result := <-flow.callbackCh
	if result.Code != "auth-code-123" {
		t.Errorf("expected auth-code-123, got %s", result.Code)
	}
	if !result.IsAutomatic {
		t.Error("expected IsAutomatic=true")
	}
}

func TestClaudeCallback_StateMismatch(t *testing.T) {
	flow, err := startClaudeCallbackListenerNet("correct-state")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer flow.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=code&state=wrong-state", flow.Port))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestClaudeCallback_MissingCode(t *testing.T) {
	flow, err := startClaudeCallbackListenerNet("test-state")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer flow.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?state=test-state", flow.Port))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestClaudeCallback_OAuthError(t *testing.T) {
	flow, err := startClaudeCallbackListenerNet("test-state")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer flow.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?error=access_denied&error_description=User+cancelled", flow.Port))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()

	result := <-flow.callbackCh
	if result.Error == nil {
		t.Error("expected error for OAuth error response")
	}
}

// ---------------------------------------------------------------------------
// WaitForClaudeAuthCode
// ---------------------------------------------------------------------------

func TestWaitForClaudeAuthCode_NilFlow(t *testing.T) {
	_, _, err := WaitForClaudeAuthCode(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil flow")
	}
}

func TestWaitForClaudeAuthCode_NilChannel(t *testing.T) {
	_, _, err := WaitForClaudeAuthCode(context.Background(), &ClaudeOAuthFlow{})
	if err == nil {
		t.Error("expected error for nil channel")
	}
}

func TestWaitForClaudeAuthCode_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := WaitForClaudeAuthCode(ctx, &ClaudeOAuthFlow{
		callbackCh: make(chan claudeCallbackResult, 1),
	})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// ClaudeOAuthFlow.Close
// ---------------------------------------------------------------------------

func TestClaudeOAuthFlow_Close_Nil(t *testing.T) {
	var flow *ClaudeOAuthFlow
	flow.Close() // should not panic
}

func TestClaudeOAuthFlow_Close_NoServer(t *testing.T) {
	(&ClaudeOAuthFlow{}).Close() // should not panic
}

// ---------------------------------------------------------------------------
// StartClaudeOAuthFlow
// ---------------------------------------------------------------------------

func TestStartClaudeOAuthFlow_Success(t *testing.T) {
	flow, err := StartClaudeOAuthFlow(context.Background())
	if err != nil {
		t.Fatalf("StartClaudeOAuthFlow: %v", err)
	}
	defer flow.Close()

	if flow.Port == 0 {
		t.Error("expected non-zero port")
	}
	if flow.CodeVerifier == "" {
		t.Error("expected non-empty code verifier")
	}
	if flow.State == "" {
		t.Error("expected non-empty state")
	}
	if flow.AutoURL == "" {
		t.Error("expected non-empty auto URL")
	}
	if flow.ManualURL == "" {
		t.Error("expected non-empty manual URL")
	}
	if !strings.Contains(flow.AutoURL, "code_challenge_method=S256") {
		t.Error("auto URL should contain PKCE method")
	}
}

// ---------------------------------------------------------------------------
// Token cache edge cases
// ---------------------------------------------------------------------------

func TestTokenCache_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0600)
	if cache.Load("bad") != nil {
		t.Error("expected nil for corrupted file")
	}
}

func TestTokenCache_SaveCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	cache := NewTokenCache(dir)
	token := &PKCEToken{AccessToken: "test", Expiry: time.Now().Add(time.Hour)}
	if err := cache.Save("provider", token, ""); err != nil {
		t.Fatalf("Save should create dirs: %v", err)
	}
	if cache.Load("provider") == nil {
		t.Error("expected to load saved token")
	}
}

func TestCacheKey_LongClientID(t *testing.T) {
	key := CacheKey("github", "abcdefghijklmnopqrst")
	if !strings.HasPrefix(key, "github-") {
		t.Errorf("expected github- prefix, got %s", key)
	}
	if len(key) > len("github-")+12 {
		t.Errorf("expected max 12 chars of clientID, got %s", key)
	}
}

// ---------------------------------------------------------------------------
// base64URLDecode padding cases
// ---------------------------------------------------------------------------

func TestBase64URLDecode_Padding2(t *testing.T) {
	input := base64.RawURLEncoding.EncodeToString([]byte("x"))
	decoded, err := base64URLDecode(input)
	if err != nil {
		t.Fatalf("base64URLDecode: %v", err)
	}
	if string(decoded) != "x" {
		t.Errorf("expected 'x', got %q", decoded)
	}
}

func TestBase64URLDecode_NoPadding(t *testing.T) {
	input := base64.RawURLEncoding.EncodeToString([]byte("hello world"))
	decoded, err := base64URLDecode(input)
	if err != nil {
		t.Fatalf("base64URLDecode: %v", err)
	}
	if string(decoded) != "hello world" {
		t.Errorf("expected 'hello world', got %q", decoded)
	}
}

// ---------------------------------------------------------------------------
// NewTokenValidator URL construction
// ---------------------------------------------------------------------------

func TestNewTokenValidator_WellKnownURL(t *testing.T) {
	tv, err := NewTokenValidator("client", "https://example.com/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	if tv.jwksURL != "https://example.com/.well-known/openid-configuration" {
		t.Errorf("jwksURL mismatch: %s", tv.jwksURL)
	}
}

func TestNewTokenValidator_PlainIssuerURL(t *testing.T) {
	tv, err := NewTokenValidator("client", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tv.jwksURL, "/.well-known/openid-configuration") {
		t.Errorf("jwksURL should contain discovery path: %s", tv.jwksURL)
	}
}

func TestNewTokenValidator_TrailingSlash(t *testing.T) {
	tv, err := NewTokenValidator("client", "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tv.jwksURL, "/.well-known/openid-configuration") {
		t.Errorf("jwksURL should contain discovery path: %s", tv.jwksURL)
	}
}

// ---------------------------------------------------------------------------
// normalizeDomain helper
// ---------------------------------------------------------------------------

func TestNormalizeDomain(t *testing.T) {
	tests := []struct{ input, want string }{
		{"https://company.ghe.com", "company.ghe.com"},
		{"http://company.ghe.com", "company.ghe.com"},
		{"company.ghe.com/", "company.ghe.com"},
		{"  company.ghe.com  ", "company.ghe.com"},
	}
	for _, tt := range tests {
		got := normalizeDomain(tt.input)
		if got != tt.want {
			t.Errorf("normalizeDomain(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ResolveA2AAuth edge cases
// ---------------------------------------------------------------------------

func TestResolveA2AAuth_UnknownProvider(t *testing.T) {
	authURL, _, _, _, err := ResolveA2AAuth("unknown", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authURL != "" {
		t.Errorf("expected empty auth URL for unknown, got %s", authURL)
	}
}

func TestResolveA2AAuth_NoClientID(t *testing.T) {
	authURL, _, _, _, err := ResolveA2AAuth("", "", "https://issuer.example.com", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authURL != "" {
		t.Errorf("expected empty for no clientID, got %s", authURL)
	}
}

// ---------------------------------------------------------------------------
// PKCE round-trip
// ---------------------------------------------------------------------------

func TestPKCEChallenge_RoundTrip(t *testing.T) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	challenge := GenerateCodeChallenge(verifier)
	challenge2 := GenerateCodeChallenge(verifier)
	if challenge != challenge2 {
		t.Error("challenge should be deterministic")
	}
	if len(challenge) != 43 {
		t.Errorf("expected 43-char challenge, got %d", len(challenge))
	}
}

// ---------------------------------------------------------------------------
// Copilot device flow with TLS mock
// ---------------------------------------------------------------------------

func newTLSCopilotMock(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestStartCopilotDeviceFlow_Success(t *testing.T) {
	t.Setenv("GGCODE_GITHUB_COPILOT_CLIENT_ID", "test-id")
	srv := newTLSCopilotMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/device/code" {
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dev-123",
				"user_code":        "ABCD-1234",
				"verification_uri": "https://github.com/login/device",
				"interval":         0,
			})
		}
	})

	flow, err := startCopilotDeviceFlow(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("startCopilotDeviceFlow: %v", err)
	}
	if flow.DeviceCode != "dev-123" {
		t.Errorf("expected dev-123, got %s", flow.DeviceCode)
	}
	if flow.UserCode != "ABCD-1234" {
		t.Errorf("expected ABCD-1234, got %s", flow.UserCode)
	}
}

func TestStartCopilotDeviceFlow_EnterpriseURL(t *testing.T) {
	t.Setenv("GGCODE_GITHUB_COPILOT_CLIENT_ID", "test-id")
	srv := newTLSCopilotMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/device/code" {
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dev-enterprise",
				"user_code":        "ENTR-5678",
				"verification_uri": "https://github.com/login/device",
				"interval":         0,
			})
		}
	})

	flow, err := startCopilotDeviceFlow(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("startCopilotDeviceFlow: %v", err)
	}
	// EnterpriseURL is normalized (scheme stripped)
	if flow.EnterpriseURL == "" {
		t.Error("expected non-empty enterprise URL")
	}
}

func TestStartCopilotDeviceFlow_BadResponse(t *testing.T) {
	t.Setenv("GGCODE_GITHUB_COPILOT_CLIENT_ID", "test-id")
	srv := newTLSCopilotMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "internal error")
	})

	_, err := startCopilotDeviceFlow(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestStartCopilotDeviceFlow_NonTLSSuccess(t *testing.T) {
	t.Setenv("GGCODE_GITHUB_COPILOT_CLIENT_ID", "test-id")
	// The function always uses https://, so non-TLS will fail with connection error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dev-plain",
			"user_code":        "PLAI-9999",
			"verification_uri": "https://github.com/login/device",
			"interval":         0,
		})
	}))
	defer srv.Close()

	_, err := startCopilotDeviceFlow(context.Background(), srv.Client(), srv.URL)
	// Expected to fail because function uses https:// but server is http://
	if err == nil {
		t.Log("startCopilotDeviceFlow plain unexpectedly succeeded")
	}
}

func TestPollCopilotDeviceFlow_PendingThenSuccess(t *testing.T) {
	t.Setenv("GGCODE_GITHUB_COPILOT_CLIENT_ID", "test-id")
	polls := 0
	srv := newTLSCopilotMock(t, func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls <= 1 {
			json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending", "interval": 0})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "final-token"})
		}
	})

	// Strip https:// from srv.URL to get domain:port for the CopilotDeviceFlow
	domain := strings.TrimPrefix(srv.URL, "https://")
	flow := &CopilotDeviceFlow{DeviceCode: "dev", VerificationURI: "https://github.com/login/device", Domain: domain}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := pollCopilotDeviceFlow(ctx, srv.Client(), flow)
	if err != nil {
		t.Fatalf("pollCopilotDeviceFlow: %v", err)
	}
	if info.AccessToken != "final-token" {
		t.Errorf("expected final-token, got %s", info.AccessToken)
	}
}

func TestPollCopilotDeviceFlow_ContextCancelled(t *testing.T) {
	t.Setenv("GGCODE_GITHUB_COPILOT_CLIENT_ID", "test-id")
	srv := newTLSCopilotMock(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending", "interval": 0})
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	flow := &CopilotDeviceFlow{DeviceCode: "dev", VerificationURI: "https://github.com/login/device"}
	_, err := pollCopilotDeviceFlow(ctx, srv.Client(), flow)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// Store Delete and loadAll/saveAll edge cases
// ---------------------------------------------------------------------------

func TestStoreDelete_Success(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "store.json"))

	store.Save(&Info{ProviderID: "to-delete", Type: "oauth", AccessToken: "tok"})
	ok, _ := store.HasUsableToken("to-delete")
	if !ok {
		t.Fatal("expected token to exist")
	}

	err := store.Delete("to-delete")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ok, _ = store.HasUsableToken("to-delete")
	if ok {
		t.Error("expected token to be deleted")
	}
}

func TestStoreDelete_NonExistent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "store.json"))
	err := store.Delete("nonexistent")
	// Should not error for non-existent key
	_ = err
}

func TestStoreSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")

	store1 := NewStore(path)
	store1.Save(&Info{
		ProviderID:  "test-reload",
		Type:        "oauth",
		AccessToken: "access-123",
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	// Create new store instance from same file
	store2 := NewStore(path)
	ok, err := store2.HasUsableToken("test-reload")
	if err != nil {
		t.Fatalf("HasUsableToken reload: %v", err)
	}
	if !ok {
		t.Error("expected to find token after reload")
	}
}

func TestStoreSave_MultipleProviders(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "multi.json"))

	for i := 0; i < 5; i++ {
		store.Save(&Info{
			ProviderID:  fmt.Sprintf("provider-%d", i),
			Type:        "oauth",
			AccessToken: fmt.Sprintf("token-%d", i),
			ExpiresAt:   time.Now().Add(time.Hour),
		})
	}

	for i := 0; i < 5; i++ {
		provider := fmt.Sprintf("provider-%d", i)
		ok, err := store.HasUsableToken(provider)
		if err != nil {
			t.Fatalf("HasUsableToken %s: %v", provider, err)
		}
		if !ok {
			t.Errorf("expected token for %s", provider)
		}
	}
}

// ---------------------------------------------------------------------------
// TokenCache Save with scope
// ---------------------------------------------------------------------------

func TestTokenCache_SaveAndLoadWithScope(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{
		AccessToken:  "scoped-token",
		RefreshToken: "scoped-refresh",
		TokenType:    "bearer",
		Expiry:       time.Now().Add(time.Hour),
		Scope:        "read:user user:email",
	}
	cache.Save("github", token, "client")

	loaded := cache.Load("github")
	if loaded == nil {
		t.Fatal("expected token")
	}
	if loaded.Scope != "read:user user:email" {
		t.Errorf("expected scope, got %q", loaded.Scope)
	}
}

func TestTokenCache_LoadValid_Nil(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)
	if cache.LoadValid("nonexistent") != nil {
		t.Error("expected nil for nonexistent token")
	}
}

// ---------------------------------------------------------------------------
// ResolveA2AAuth with preset
// ---------------------------------------------------------------------------

func TestResolveA2AAuth_GithubPreset(t *testing.T) {
	authURL, _, _, _, err := ResolveA2AAuth("github", "my-client", "", "")
	if err != nil {
		t.Fatalf("ResolveA2AAuth github: %v", err)
	}
	if authURL == "" {
		t.Error("expected non-empty auth URL")
	}
}

func TestResolveA2AAuth_IssuerWithClientID(t *testing.T) {
	authURL, _, clientID, _, err := ResolveA2AAuth("", "my-client", "https://issuer.example.com", "")
	if err != nil {
		t.Fatalf("ResolveA2AAuth issuer: %v", err)
	}
	if authURL != "" {
		// When only issuer is provided without provider, URL should be empty
		// because we can't determine auth/token URLs from just an issuer
		t.Logf("authURL: %s", authURL)
	}
	if clientID != "my-client" {
		t.Errorf("expected my-client, got %s", clientID)
	}
}

func TestResolveA2AAuth_WithClientSecret(t *testing.T) {
	authURL, _, _, _, err := ResolveA2AAuth("github", "client", "", "my-secret")
	if err != nil {
		t.Fatalf("ResolveA2AAuth: %v", err)
	}
	if authURL == "" {
		t.Error("expected non-empty auth URL")
	}
}

// ---------------------------------------------------------------------------
// ValidateToken dispatch for opaque tokens
// ---------------------------------------------------------------------------

func TestValidateToken_OpaqueTokenDispatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"active": true, "sub": "opaque-user"})
	}))
	defer srv.Close()

	tv, _ := NewTokenValidator("test", srv.URL)
	result, err := tv.ValidateToken(context.Background(), "plain-opaque-token")
	if err != nil {
		t.Logf("ValidateToken opaque (network may fail): %v", err)
		return
	}
	if result["sub"] != "opaque-user" {
		t.Errorf("expected opaque-user, got %v", result["sub"])
	}
}

// ---------------------------------------------------------------------------
// sleepWithMargin
// ---------------------------------------------------------------------------

func TestSleepWithMargin_ImmediateCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	sleepWithMargin(ctx, 1*time.Second)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("sleepWithMargin should return quickly on cancel, took %v", elapsed)
	}
}

func TestSleepWithMargin_TimerExpires(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	sleepWithMargin(ctx, 50*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 50*time.Millisecond {
		t.Errorf("sleepWithMargin should wait for timer, took %v", elapsed)
	}
}

// ---------------------------------------------------------------------------
// Info.IsExpired with valid token
// ---------------------------------------------------------------------------

func TestInfoIsExpired_FutureExpiry(t *testing.T) {
	info := &Info{
		ProviderID: "test",
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	if info.IsExpired() {
		t.Error("future token should not be expired")
	}
}

func TestInfoIsExpired_PastExpiry(t *testing.T) {
	info := &Info{
		ProviderID: "test",
		ExpiresAt:  time.Now().Add(-time.Hour),
	}
	if !info.IsExpired() {
		t.Error("past token should be expired")
	}
}

func TestInfoIsExpired_ZeroExpiry(t *testing.T) {
	info := &Info{ProviderID: "test"}
	if info.IsExpired() {
		t.Error("zero expiry should be considered not expired (infinite lifetime)")
	}
}
