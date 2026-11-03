package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func rsaJWKSBody(t *testing.T, kid string, pub *rsa.PublicKey) string {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(bigNewIntBytes(pub.E))
	return fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":%q,"use":"sig","alg":"RS256","n":%q,"e":%q}]}`, kid, n, e)
}

func bigNewIntBytes(e int) []byte {
	return big.NewInt(int64(e)).Bytes()
}

func newJWKSTestServer(t *testing.T, kid string, pub *rsa.PublicKey) (*TokenValidator, func()) {
	t.Helper()
	jwksBody := rsaJWKSBody(t, kid, pub)
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, jwksBody)
	})
	server := httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": "https://swr.example.com", "jwks_uri": server.URL + "/jwks",
		})
	})
	tv, err := NewTokenValidator("client", server.URL+"/.well-known/openid-configuration")
	if err != nil {
		server.Close()
		t.Fatalf("NewTokenValidator: %v", err)
	}
	return tv, server.Close
}

func expireJWKSCache(t *testing.T, tv *TokenValidator) {
	t.Helper()
	tv.mu.Lock()
	tv.jwksExp = time.Now().Add(-time.Minute)
	tv.mu.Unlock()
}

func TestJWKSStaleWhileRevalidate_ServesStaleKeysOnRefreshFailure(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tv, closeServer := newJWKSTestServer(t, "swr-kid", &key.PublicKey)
	defer closeServer()

	got, err := tv.getPublicKey(context.Background(), "swr-kid")
	if err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	closeServer()

	expireJWKSCache(t, tv)

	got2, err := tv.getPublicKey(context.Background(), "swr-kid")
	if err != nil {
		t.Fatalf("expected stale fallback after refresh failure, got error: %v", err)
	}
	rsaGot, ok1 := got.(*rsa.PublicKey)
	rsaGot2, ok2 := got2.(*rsa.PublicKey)
	if !ok1 || !ok2 || rsaGot.N.Cmp(rsaGot2.N) != 0 {
		t.Errorf("stale key differs from originally cached key")
	}
}

func TestJWKSStaleFallback_BoundedStaleness(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tv, closeServer := newJWKSTestServer(t, "old-kid", &key.PublicKey)
	defer closeServer()

	if _, err := tv.getPublicKey(context.Background(), "old-kid"); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	closeServer()

	tv.mu.Lock()
	tv.jwksExp = time.Now().Add(-time.Minute)
	tv.jwksFetchedAt = time.Now().Add(-maxJWKSStaleAge - time.Hour)
	tv.mu.Unlock()

	if _, err := tv.getPublicKey(context.Background(), "old-kid"); err == nil {
		t.Errorf("expected error once the stale window lapses, got a key")
	}
}

func TestJWKSStaleFallback_UnknownKidStillErrors(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tv, closeServer := newJWKSTestServer(t, "known-kid", &key.PublicKey)
	defer closeServer()

	if _, err := tv.getPublicKey(context.Background(), "known-kid"); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	closeServer()

	expireJWKSCache(t, tv)

	_, err = tv.getPublicKey(context.Background(), "never-seen-kid")
	if err == nil {
		t.Errorf("expected error for unknown kid even with stale fallback active")
	}
}
