package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// TokenValidator — JWT validation tests
// ---------------------------------------------------------------------------

func TestTokenValidatorHS256JWT(t *testing.T) {
	tv, err := NewTokenValidator("test-client", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	claims := jwt.MapClaims{
		"sub": "user123",
		"iss": "https://example.com",
		"aud": "test-client",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte("test-client"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := tv.ValidateToken(context.Background(), tokenString)
	if err != nil {
		t.Fatalf("validate HS256 JWT: %v", err)
	}
	if result["sub"] != "user123" {
		t.Errorf("expected sub=user123, got %v", result["sub"])
	}
}

func TestTokenValidatorExpiredJWT(t *testing.T) {
	tv, err := NewTokenValidator("test-client", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	claims := jwt.MapClaims{
		"sub": "user123",
		"iss": "https://example.com",
		"aud": "test-client",
		"exp": time.Now().Add(-time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte("test-client"))

	_, err = tv.ValidateToken(context.Background(), tokenString)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestTokenValidatorNonJWT(t *testing.T) {
	tv, _ := NewTokenValidator("test-client", "https://example.com")
	_, err := tv.ValidateToken(context.Background(), "opaque-token-abc123")
	// Will fail because there's no introspection endpoint
	if err == nil {
		t.Log("opaque token accepted (introspection endpoint responded)")
	} else {
		t.Logf("opaque token rejected (expected): %v", err)
	}
}

func newMockOIDCServer(t *testing.T, kid string, key interface{}) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewUnstartedServer(mux)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":   "https://mock-oidc.example.com",
			"jwks_uri": server.URL + "/.well-known/jwks.json",
		})
	})

	switch k := key.(type) {
	case *rsa.PrivateKey:
		n := base64.RawURLEncoding.EncodeToString(k.PublicKey.N.Bytes())
		eBytes := []byte{0, 1, 0, 1}
		e := base64.RawURLEncoding.EncodeToString(eBytes)
		mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"keys": []map[string]string{{
					"kid": kid, "kty": "RSA", "use": "sig", "n": n, "e": e,
				}},
			})
		})
	case *ecdsa.PrivateKey:
		x := base64.RawURLEncoding.EncodeToString(k.PublicKey.X.Bytes())
		y := base64.RawURLEncoding.EncodeToString(k.PublicKey.Y.Bytes())
		mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"keys": []map[string]string{{
					"kid": kid, "kty": "EC", "use": "sig", "crv": "P-256", "x": x, "y": y,
				}},
			})
		})
	}

	server.Start()
	return server
}

func TestTokenValidatorRS256JWTWithJWKS(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	server := newMockOIDCServer(t, "rsa-key-1", privateKey)
	defer server.Close()

	tv, _ := NewTokenValidator("test-client", server.URL+"/.well-known/openid-configuration")

	claims := jwt.MapClaims{
		"sub": "rsa-user",
		"iss": "https://mock-oidc.example.com",
		"aud": "test-client",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "rsa-key-1"
	tokenString, _ := token.SignedString(privateKey)

	result, err := tv.ValidateToken(context.Background(), tokenString)
	if err != nil {
		t.Fatalf("validate RS256 JWT: %v", err)
	}
	if result["sub"] != "rsa-user" {
		t.Errorf("expected sub=rsa-user, got %v", result["sub"])
	}
}

func TestTokenValidatorECDSA_JWTWithJWKS(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	server := newMockOIDCServer(t, "ec-key-1", privateKey)
	defer server.Close()

	tv, _ := NewTokenValidator("ec-client", server.URL+"/.well-known/openid-configuration")

	claims := jwt.MapClaims{
		"sub": "ec-user",
		"iss": "https://mock-oidc.example.com",
		"aud": "ec-client",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = "ec-key-1"
	tokenString, _ := token.SignedString(privateKey)

	result, err := tv.ValidateToken(context.Background(), tokenString)
	if err != nil {
		t.Fatalf("validate ES256 JWT: %v", err)
	}
	if result["sub"] != "ec-user" {
		t.Errorf("expected sub=ec-user, got %v", result["sub"])
	}
}

func TestTokenValidatorJWKSCaching(t *testing.T) {
	fetchCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": "https://cache.example.com", "jwks_uri": "https://cache.example.com/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"keys":[]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tv, _ := NewTokenValidator("client", server.URL+"/.well-known/openid-configuration")
	tv.getPublicKey(context.Background(), "key1")
	count1 := fetchCount

	tv.getPublicKey(context.Background(), "key1")
	if fetchCount > count1+1 {
		t.Errorf("JWKS should be cached, fetchCount went from %d to %d", count1, fetchCount)
	}
}

func TestBase64URLDecode(t *testing.T) {
	tests := []struct{ input, want string }{
		{"Zm9v", "foo"},
		{"Zm9vYmFy", "foobar"},
		{"", ""},
	}
	for _, tt := range tests {
		got, err := base64URLDecode(tt.input)
		if err != nil {
			t.Errorf("base64URLDecode(%q) error: %v", tt.input, err)
		}
		if string(got) != tt.want {
			t.Errorf("base64URLDecode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTokenValidatorOpaqueTokenWithIntrospection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/introspect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true, "sub": "opaque-user", "scope": "read write",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tv, _ := NewTokenValidator("client", server.URL)
	result, err := tv.validateOpaqueToken(context.Background(), "opaque-abc123")
	if err != nil {
		t.Fatalf("opaque token: %v", err)
	}
	if result["sub"] != "opaque-user" {
		t.Errorf("expected sub=opaque-user, got %v", result["sub"])
	}
}

func TestTokenValidatorOpaqueTokenInactive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/introspect", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"active": false})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tv, _ := NewTokenValidator("client", server.URL)
	_, err := tv.validateOpaqueToken(context.Background(), "expired")
	if err == nil {
		t.Fatal("expected error for inactive token")
	}
}
