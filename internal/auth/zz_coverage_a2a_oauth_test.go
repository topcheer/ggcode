package auth

// Coverage suite for the offline-testable paths of a2a_oauth.go: token
// exchange parsing, device-flow polling, mTLS config building, token
// provider cache hits and preset resolution. Network endpoints are
// replaced by httptest servers; browser/clipboard side-effect paths are
// intentionally not exercised.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExchangeCodeForTokenJSONResponse(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"bearer","scope":"s","expires_in":3600}`))
	}))
	defer srv.Close()

	tok, err := exchangeCodeForToken(context.Background(), A2AOAuth2Config{ClientID: "cid", ClientSecret: "sec", TokenURL: srv.URL}, "thecode", "http://localhost:1/callback", "ver")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" || tok.TokenType != "bearer" || tok.Scope != "s" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if time.Until(tok.Expiry) <= 0 || time.Until(tok.Expiry) > time.Hour {
		t.Fatalf("expiry not set from expires_in: %v", tok.Expiry)
	}
	if !strings.Contains(gotBody, `"code_verifier":"ver"`) || !strings.Contains(gotBody, `"client_secret":"sec"`) {
		t.Fatalf("request body missing verifier/secret: %s", gotBody)
	}
}

func TestExchangeCodeForTokenURLEncodedFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("access_token=fmt&refresh_token=frt&token_type=bearer&expires_in=60"))
	}))
	defer srv.Close()

	tok, err := exchangeCodeForToken(context.Background(), A2AOAuth2Config{ClientID: "cid", TokenURL: srv.URL}, "c", "r", "v")
	if err != nil {
		t.Fatalf("url-encoded fallback failed: %v", err)
	}
	if tok.AccessToken != "fmt" || tok.RefreshToken != "frt" {
		t.Fatalf("unexpected token: %+v", tok)
	}
}

func TestExchangeCodeForTokenHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad_grant", http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := exchangeCodeForToken(context.Background(), A2AOAuth2Config{TokenURL: srv.URL}, "c", "r", "v"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 error, got %v", err)
	}
}

func TestExchangeCodeForTokenUnparseableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json and not url-encoded \x00"))
	}))
	defer srv.Close()

	if _, err := exchangeCodeForToken(context.Background(), A2AOAuth2Config{TokenURL: srv.URL}, "c", "r", "v"); err == nil {
		t.Fatal("expected parse error for garbage body")
	}
}

func TestPollDeviceTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"dt","refresh_token":"drt","token_type":"bearer","expires_in":120}`))
	}))
	defer srv.Close()

	tok, err := pollDeviceToken(context.Background(), A2AOAuth2Config{ClientID: "cid", TokenURL: srv.URL}, "dev")
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}
	if tok.AccessToken != "dt" || tok.RefreshToken != "drt" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if time.Until(tok.Expiry) <= 0 {
		t.Fatal("expiry not set")
	}
}

func TestPollDeviceTokenErrorPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"expired_token","error_description":"code expired"}`))
	}))
	defer srv.Close()

	_, err := pollDeviceToken(context.Background(), A2AOAuth2Config{TokenURL: srv.URL}, "dev")
	if err == nil || !strings.Contains(err.Error(), "expired_token") || !strings.Contains(err.Error(), "code expired") {
		t.Fatalf("expected error payload surfaced, got %v", err)
	}
}

func TestStartDeviceFlowDeviceCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"nope"}`))
	}))
	defer srv.Close()

	_, err := StartDeviceFlow(context.Background(), A2AOAuth2Config{AuthorizeURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("expected device code error, got %v", err)
	}
}

func TestStartDeviceFlowBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("garbage"))
	}))
	defer srv.Close()

	_, err := StartDeviceFlow(context.Background(), A2AOAuth2Config{AuthorizeURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "parse device response") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

// genTestCert writes a self-signed certificate and key to dir and returns
// their paths plus the PEM of the CA (= the same cert).
func genTestCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ggcode-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	_ = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return certPath, keyPath
}

func TestBuildTLSConfigSuccess(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genTestCert(t, dir)
	cfg, err := (&MTLSConfig{CertFile: certPath, KeyFile: keyPath, CAFile: certPath}).BuildTLSConfig()
	if err != nil {
		t.Fatalf("BuildTLSConfig failed: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert || cfg.MinVersion < tls.VersionTLS12 {
		t.Fatalf("unexpected tls config: %+v", cfg)
	}
}

func TestBuildTLSConfigErrors(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genTestCert(t, dir)

	if _, err := (&MTLSConfig{CertFile: filepath.Join(dir, "missing.pem"), KeyFile: keyPath}).BuildTLSConfig(); err == nil {
		t.Fatal("expected error for missing cert file")
	}
	badCA := filepath.Join(dir, "bad-ca.pem")
	_ = os.WriteFile(badCA, []byte("not a cert"), 0o600)
	if _, err := (&MTLSConfig{CertFile: certPath, KeyFile: keyPath, CAFile: badCA}).BuildTLSConfig(); err == nil {
		t.Fatal("expected error for non-PEM CA file")
	}
}

func TestWithValidIssuers(t *testing.T) {
	tv := &TokenValidator{}
	WithValidIssuers([]string{"https://extra", ""})(tv)
	if len(tv.validIssuers) != 2 || tv.validIssuers[0] != "https://extra" {
		t.Fatalf("option did not apply: %+v", tv.validIssuers)
	}
}

func TestPKCETokenProviderCacheHit(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)
	cacheKey := CacheKey("prov-x", "cid")
	if err := cache.Save(cacheKey, &PKCEToken{AccessToken: "cached-at", RefreshToken: "cached-rt", Expiry: time.Now().Add(time.Hour)}, "cid"); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	p := &PKCETokenProvider{Provider: "prov-x", Cache: cache}
	// Point the config at a dead URL: any cache miss would fail loudly.
	p.Config = A2AOAuth2Config{ClientID: "cid", TokenURL: "http://127.0.0.1:1/dead", AuthorizeURL: "http://127.0.0.1:1/dead"}
	at, rt, _, err := p.GetToken(context.Background())
	if err != nil {
		t.Fatalf("cache hit must not hit network: %v", err)
	}
	if at != "cached-at" || rt != "cached-rt" {
		t.Fatalf("unexpected cached token: %q/%q", at, rt)
	}
}

func TestDeviceFlowTokenProviderCacheHit(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)
	cacheKey := CacheKey("prov-y", "cid")
	if err := cache.Save(cacheKey, &PKCEToken{AccessToken: "d-at", RefreshToken: "d-rt", Expiry: time.Now().Add(time.Hour)}, "cid"); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	p := &DeviceFlowTokenProvider{Provider: "prov-y", Cache: cache}
	p.Config = A2AOAuth2Config{ClientID: "cid", TokenURL: "http://127.0.0.1:1/dead", AuthorizeURL: "http://127.0.0.1:1/dead"}
	at, rt, _, err := p.GetToken(context.Background())
	if err != nil {
		t.Fatalf("cache hit must not hit network: %v", err)
	}
	if at != "d-at" || rt != "d-rt" {
		t.Fatalf("unexpected cached token: %q/%q", at, rt)
	}
}

func TestNewTokenProviderFromPreset(t *testing.T) {
	// Unknown provider errors out without touching the network.
	if _, err := NewTokenProviderFromPreset("no-such-provider", "", false); err == nil {
		t.Fatal("expected error for unknown provider")
	}

	headless, err := NewTokenProviderFromPreset("github", "", true)
	if err != nil {
		t.Fatalf("headless preset: %v", err)
	}
	if _, ok := headless.(*DeviceFlowTokenProvider); !ok {
		t.Fatalf("headless must select DeviceFlowTokenProvider, got %T", headless)
	}

	desktop, err := NewTokenProviderFromPreset("github", "", false)
	if err != nil {
		t.Fatalf("desktop preset: %v", err)
	}
	if _, ok := desktop.(*PKCETokenProvider); !ok {
		t.Fatalf("desktop must select PKCETokenProvider, got %T", desktop)
	}
}
